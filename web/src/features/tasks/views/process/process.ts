/**
 * The task's work as a process: turns, their attempts and the steps the worker took (tool calls, files read or
 * edited outside a tool, the final report), each step classified into a stage. Tree, Graph and Files all read
 * this model; it is built from the Terminal's timeline items (so tool calls are already paired with their
 * results and files) plus the turn records of GET /tasks/{id}.
 */
import type { Turn, WorkerTool } from '@/api/types'

import type { TimelineItem } from '../terminal/timeline'

export const STAGES = ['explore', 'edit', 'verify', 'run', 'report'] as const
export type Stage = (typeof STAGES)[number]

export type StepState = 'running' | 'ok' | 'failed'

export interface Step {
  /** The timeline item key: the Terminal can scroll to it. */
  key: string
  turn: number
  attempt: number
  stage: Stage
  name: string
  /** A command, a file path or a short input summary. */
  label: string
  /** The file it touched, as the worker reported it (absolute or cwd-relative). */
  path?: string
  op?: 'read' | 'edit'
  state: StepState
  /** ms since epoch. */
  start: number
  /** 0 while running (views measure it against the clock) or when the worker did not report it. */
  durationMs: number
}

export interface AttemptNode {
  n: number
  accountLabel?: string
  model?: string
  status?: string
  steps: Step[]
}

export interface TurnNode {
  n: number
  by?: Turn['by']
  message?: string
  status?: Turn['status']
  startedAt?: string
  endedAt?: string
  attempts: AttemptNode[]
}

const EXPLORE = /read|view|list|ls\b|glob|grep|search|find|open|fetch|browse|inspect/i
const EDIT = /edit|write|create|replace|patch|apply|delete|remove|move|rename|insert/i
const SHELL = /command|shell|bash|exec|terminal|run/i
const VERIFY =
  /\b(test|tests|vet|lint|check|tsc|typecheck|build|pytest|jest|vitest|mocha|clippy|eslint|ruff|mypy|golangci-lint|prettier --check)\b/i

/** The stage of a tool call, from its name and (for shell calls) its command. */
export function stageOf(tool: Pick<WorkerTool, 'name' | 'command'>): Stage {
  if (tool.command || SHELL.test(tool.name)) {
    return VERIFY.test(tool.command ?? '') ? 'verify' : 'run'
  }
  if (EDIT.test(tool.name)) return 'edit'
  if (EXPLORE.test(tool.name)) return 'explore'
  return 'run'
}

function inputPath(input: unknown): string | undefined {
  if (!input || typeof input !== 'object' || !('path' in input)) return undefined
  const value = (input as { path: unknown }).path
  return typeof value === 'string' ? value : undefined
}

function stepOf(item: TimelineItem): Step | null {
  const base = {
    key: item.key,
    turn: item.turn ?? 0,
    attempt: item.attempt ?? 1,
    start: Date.parse(item.ts) || 0,
  }
  switch (item.kind) {
    case 'tool': {
      const tool = item.tool
      const path = item.file?.path ?? inputPath(tool.input)
      const state: StepState =
        tool.state === 'calling'
          ? 'running'
          : tool.state === 'failed' || tool.blocked
            ? 'failed'
            : 'ok'
      return {
        ...base,
        stage: item.file?.op === 'edit' ? 'edit' : stageOf(tool),
        name: tool.name,
        label: tool.command ?? path ?? '',
        path,
        op: item.file?.op,
        state,
        durationMs: tool.durationMs ?? 0,
      }
    }
    case 'file':
      return {
        ...base,
        stage: item.op === 'edit' ? 'edit' : 'explore',
        name: item.op,
        label: item.path,
        path: item.path,
        op: item.op,
        state: 'ok',
        durationMs: 0,
      }
    case 'report':
      return {
        ...base,
        stage: 'report',
        name: 'report',
        label: item.report.response?.split('\n', 1)[0] ?? '',
        state: item.report.status === 'success' ? 'ok' : 'failed',
        durationMs: 0,
      }
    default:
      return null
  }
}

/** Steps already made for an unchanged timeline item, so views can skip re-rendering them. */
export type StepCache = WeakMap<TimelineItem, Step | null>

/** Turns (from the detail when known) with their attempts and steps, in order. */
export function buildProcess(
  items: readonly TimelineItem[],
  turns: readonly Turn[] | undefined,
  cache: StepCache = new WeakMap(),
): TurnNode[] {
  const byTurn = new Map<number, TurnNode>()
  const turnNode = (n: number): TurnNode => {
    let node = byTurn.get(n)
    if (!node) {
      node = { n, attempts: [] }
      byTurn.set(n, node)
    }
    return node
  }
  const attemptNode = (turn: TurnNode, n: number): AttemptNode => {
    let node = turn.attempts.find((a) => a.n === n)
    if (!node) {
      node = { n, steps: [] }
      turn.attempts.push(node)
    }
    return node
  }

  for (const turn of turns ?? []) {
    const node = turnNode(turn.n)
    Object.assign(node, {
      by: turn.by,
      message: turn.message,
      status: turn.status,
      startedAt: turn.startedAt,
      endedAt: turn.endedAt,
    })
    turn.attempts.forEach((attempt, i) => {
      Object.assign(attemptNode(node, i + 1), {
        accountLabel: attempt.accountLabel,
        model: attempt.model,
        status: attempt.status,
      })
    })
  }
  for (const item of items) {
    let step = cache.get(item)
    if (step === undefined) {
      step = stepOf(item)
      cache.set(item, step)
    }
    if (!step || !step.turn) continue
    attemptNode(turnNode(step.turn), step.attempt).steps.push(step)
  }
  const list = [...byTurn.values()].sort((a, b) => a.n - b.n)
  for (const turn of list) turn.attempts.sort((a, b) => a.n - b.n)
  return list
}

export const allSteps = (turns: readonly TurnNode[]): Step[] =>
  turns.flatMap((turn) => turn.attempts.flatMap((attempt) => attempt.steps))

export function stageCounts(steps: readonly Step[]): Record<Stage, number> {
  const counts = { explore: 0, edit: 0, verify: 0, run: 0, report: 0 }
  for (const step of steps) counts[step.stage]++
  return counts
}
