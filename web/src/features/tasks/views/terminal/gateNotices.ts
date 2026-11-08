/**
 * Transcript notices of the gate, lane and verify events (docs/gates.md). Only what the events carry is shown as
 * detail: the steps (named by their kind in the reader's language), commands, their output tail, paths and the
 * verifier's reasons.
 */
import type { TaskEvent } from '@/api/events'
import type { GateResult, GateStep } from '@/api/types'
import type { IconName } from '@/components/ui/icons'

import { causeNames, LOW_MEMORY_STOPS_MAX, stepKindKey, stepWhat } from '../../detail/gate'
import type { DetailLine, Message, Tone } from './timeline'

export interface GateNotice {
  tone: Tone
  icon: IconName
  message: Message
  note?: Message
  detail?: string
  lines?: DetailLine[]
}

type Of<T extends string> = Extract<TaskEvent, { type: T }>

const GATE_TONE: Record<GateResult['state'], Tone> = {
  running: 'dim',
  green: 'ok',
  red: 'error',
  error: 'error',
  skipped: 'dim',
}

const GATE_ICON: Record<GateResult['state'], IconName> = {
  running: 'gates',
  green: 'check',
  red: 'warning',
  error: 'error',
  skipped: 'statusCancelled',
}

type StepLike = Parameters<typeof stepWhat>[0]

/** One step as a line: its kind in the reader's language, then the command or the check's id. */
function stepLine(step: StepLike): DetailLine {
  const key = stepKindKey(step)
  const what = stepWhat(step)
  return key ? { label: { key }, text: what || undefined } : { text: what || step.label }
}

/** What a failing step found, as plain lines under its own line: files, failures or the output tail. */
function stepFindings(s: GateStep): string[] {
  if (s.kind === 'lane') return s.files ?? []
  if (s.failures?.length) return s.failures.map((f) => `${f.file}: ${f.message}`)
  return s.tail ? s.tail.trimEnd().split('\n') : []
}

/**
 * The failing steps of a gate: each named, then what it found. A foreign step (it keeps the gate red) also says
 * which tasks may have caused it.
 */
export function failingLines(result: GateResult): DetailLine[] {
  const failing = result.steps.filter((s) => s.state === 'red' || s.state === 'error')
  return failing.flatMap((s, i) => [
    ...(i ? [{ text: '' }] : []),
    stepLine(s),
    ...(s.foreign
      ? [{ label: { key: 'task.gate.foreign', params: { tasks: causeNames(s.causedBy) } } }]
      : []),
    ...stepFindings(s).map((text) => ({ text })),
  ])
}

export function gateNotice(event: Of<'gate'>): GateNotice | null {
  const data = event.data
  switch (data.phase) {
    case 'start': {
      const steps: StepLike[] = data.plan?.length
        ? data.plan
        : (data.steps ?? []).map((label) => ({ label }))
      return {
        tone: 'dim',
        icon: 'gates',
        message: { key: 'terminal.ev.gate.start', params: { count: steps.length } },
        lines: steps.length ? steps.map(stepLine) : undefined,
      }
    }
    case 'settling':
      return { tone: 'warn', icon: 'statusWaiting', message: { key: 'terminal.ev.gate.settling' } }
    case 'queued':
      return queuedNotice(data)
    case 'step': {
      // Step progress shows in the gate panel; a step the memory watchdog stopped is worth a line of its own.
      const step = data.step
      if (step?.stopped !== 'low_memory') return null
      return {
        tone: 'warn',
        icon: 'gauge',
        message: {
          key: 'terminal.ev.gate.stoppedLowMemory',
          params: { n: step.stops ?? 1, max: LOW_MEMORY_STOPS_MAX },
        },
        lines: [stepLine(step)],
      }
    }
    case 'result': {
      const result = data.result
      if (!result) return null
      const lines = failingLines(result)
      return {
        tone: GATE_TONE[result.state],
        icon: GATE_ICON[result.state],
        message: { key: `terminal.ev.gate.${result.state}` },
        note:
          result.reason === 'low_memory'
            ? { key: 'terminal.ev.gate.lowMemory', params: { max: LOW_MEMORY_STOPS_MAX } }
            : result.autoFix
              ? { key: 'terminal.ev.gate.autoFix', params: { n: result.autoFix } }
              : undefined,
        lines: lines.length ? lines : undefined,
      }
    }
  }
}

/** A gate waiting to start: for free memory, or for a slot (gateConcurrency) with its place in line. */
function queuedNotice(data: Of<'gate'>['data']): GateNotice {
  if (data.wait === 'memory') {
    return { tone: 'warn', icon: 'gauge', message: { key: 'terminal.ev.gate.queuedMemory' } }
  }
  return {
    tone: 'dim',
    icon: 'statusQueued',
    message:
      data.position && data.limit
        ? {
            key: 'terminal.ev.gate.queuedSlot',
            params: { position: data.position, limit: data.limit },
          }
        : { key: 'terminal.ev.gate.queued' },
  }
}

export function laneNotice(event: Of<'lane'>): GateNotice {
  const data = event.data
  const lines =
    data.kind === 'violation'
      ? (data.files ?? [])
      : (data.conflicts ?? []).map(
          (c) => `${c.taskId} · ${c.title}: ${c.paths.map((p) => p.theirs).join(', ')}`,
        )
  return {
    tone: data.kind === 'warning' ? 'warn' : 'error',
    icon: 'warning',
    message: { key: `terminal.ev.lane.${data.kind}`, params: { count: lines.length } },
    detail: lines.join('\n') || undefined,
  }
}

export function verifyNotice(event: Of<'verify'>): GateNotice | null {
  const data = event.data
  if (data.phase === 'start') {
    return { tone: 'info', icon: 'search', message: { key: 'terminal.ev.verify.start' } }
  }
  const result = data.result
  if (!result || result.verdict === 'running') return null
  return {
    tone: result.verdict === 'accept' ? 'ok' : result.verdict === 'reject' ? 'warn' : 'error',
    icon: result.verdict === 'accept' ? 'check' : 'warning',
    message: { key: `terminal.ev.verify.${result.verdict}` },
    detail: result.reasons.join('\n') || undefined,
  }
}
