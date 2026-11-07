/**
 * Pure helpers of the contest page: who can win, who can still be applied by hand, and how a scoreboard row's
 * status, gate and verifier read (the words and tones of the task views).
 */
import type { Contest, ContestVariant, ScoreRow } from '@/api/types'
import type { TaskStatus as DisplayStatus, Tone } from '@/components/ui/tone'
import type { GateState } from '@/features/tasks/detail/gate'
import { gateTone } from '@/features/tasks/detail/gate'

/** Labels of the variants that can win (not disqualified), in the scoreboard's order. */
export function qualifiedLabels(c: Contest): string[] {
  return c.scoreboard.filter((r) => !r.disqualified).map((r) => r.label)
}

/**
 * The winner a pick applies unless the person chooses another: the proposal when it is qualified, else the first
 * qualified variant ('' when none qualifies, so a pick never defaults to a disqualified one).
 */
export function defaultWinner(c: Contest): string {
  const ok = qualifiedLabels(c)
  const proposed = c.proposal?.winner
  return proposed && ok.includes(proposed) ? proposed : (ok[0] ?? '')
}

/** Disqualified variants the daemon still applies when named (finished work with changes). */
const PICKABLE_OUT: ReadonlySet<string> = new Set(['gate_red', 'lane_violation', 'rejected'])

export function pickableOut(c: Contest): ScoreRow[] {
  return c.scoreboard.filter((r) => r.disqualified && PICKABLE_OUT.has(r.disqualified))
}

/** A task status as the daemon names it, to the display status of the task views (null: unknown). */
const RAW_STATUS: Readonly<Record<string, DisplayStatus>> = {
  queued: 'queued',
  running: 'running',
  waiting_quota: 'waiting',
  waiting_deps: 'waitingDeps',
  blocked_deps: 'blockedDeps',
  failed: 'failed',
  cancelled: 'cancelled',
}

/**
 * A variant's status: an active or failed task reads as in the task views; a finished one is "finished"
 * (neutral, since its review is the pick, not a step per row); a missing task is muted.
 */
export function variantStatus(
  status: string | undefined,
): { status: DisplayStatus } | { tone: Tone; key: string } {
  const known = status ? RAW_STATUS[status] : undefined
  if (known) return { status: known }
  if (status === 'done') return { tone: 'neutral', key: 'team.contests.rowStatus.done' }
  return { tone: 'muted', key: 'team.contests.rowStatus.missing' }
}

const GATE_WORD: Readonly<Record<string, string>> = {
  green: 'green',
  red: 'red',
  error: 'error',
  running: 'running',
}

/** The gate cell: the step words of the Gate panel ("Passed", "Failed") and its tones. */
export function gateCell(gate: string): { key: string; tone: Tone } {
  const word = GATE_WORD[gate] ?? 'not_run'
  return {
    key: `task.gate.stepState.${word}`,
    tone: word === 'not_run' ? 'muted' : gateTone(gate as GateState, null),
  }
}

/** The verifier cell: the verdict words of the Gate panel. */
export function verifierCell(verdict: string | undefined): { key: string; tone: Tone } | null {
  switch (verdict) {
    case 'accept':
      return { key: 'task.gate.verifier.verdict.accept', tone: 'success' }
    case 'reject':
      return { key: 'task.gate.verifier.verdict.reject', tone: 'danger' }
    case 'running':
      return { key: 'task.gate.verifier.verdict.running', tone: 'running' }
    case 'error':
      return { key: 'task.gate.verifier.verdict.error', tone: 'neutral' }
    default:
      return null
  }
}

/** What a variant runs on: member, provider or backend, model. */
export function runsOn(
  v: Pick<ScoreRow, 'member' | 'backend' | 'model'> & { provider?: string },
): string {
  return [v.member, v.provider ?? v.backend, v.model].filter((x): x is string => !!x).join(' · ')
}

/** The judge's anonymous letters in order, each with its variant ("A = V1"). */
export function letterLegend(letters: Readonly<Record<string, string>>): string[] {
  return Object.keys(letters)
    .sort()
    .map((l) => `${l} = ${letters[l]}`)
}

export function variantRunsOn(v: ContestVariant): string {
  return runsOn({
    member: v.member ?? v.spec.member,
    backend: v.backend ?? v.spec.backend,
    model: v.model ?? v.spec.model,
    provider: v.spec.provider,
  })
}
