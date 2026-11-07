/**
 * The gate of the open task, for the gate panel and the header chip. Everything comes from the task as the daemon
 * stores it now (summary.gate, the turns' gate and verifier, detail.proof), never from replayed events, so an old
 * gate event can never overwrite the current state (old bug: chips replayed from history).
 */
import type {
  GateResult,
  GateStep,
  TaskDetail,
  TaskProof,
  TaskSummary,
  Turn,
  VerifierResult,
} from '@/api/types'
import type { Tone } from '@/components/ui/tone'

export type GateState = GateResult['state']
export type GatePhase = NonNullable<NonNullable<TaskSummary['gate']>['phase']>
export type StepState = GateStep['state']

export interface GateView {
  /** What the gate is doing now (running, settling, verifying), or null when it is not at work. */
  phase: GatePhase | null
  /** The state of the latest gate (summary first, else the latest turn's), or null when none ran yet. */
  state: GateState | null
  /** The turn the latest gate is about. */
  turn: number | null
  result: GateResult | null
  verifier: VerifierResult | null
  proof: TaskProof | null
  /** Automatic fix turns since the lead's last instruction. */
  fixTurns: number
}

/** The latest turn that ran a gate (or has a verifier verdict). */
function latestGated(turns: readonly Turn[]): Turn | null {
  for (let i = turns.length - 1; i >= 0; i--) {
    const turn = turns[i]
    if (turn && (turn.gate || turn.verifier)) return turn
  }
  return null
}

/** Fix turns opened by the gate or the verifier after the last lead or user turn. */
export function fixTurnsOf(turns: readonly Turn[]): number {
  let n = 0
  for (let i = turns.length - 1; i >= 0; i--) {
    const by = turns[i]?.by
    if (by !== 'gate' && by !== 'verifier') break
    n++
  }
  return n
}

export function gateOf(summary: TaskSummary, detail: TaskDetail | null): GateView {
  const turns = detail?.turns ?? []
  const latest = latestGated(turns)
  const result = latest?.gate ?? null
  return {
    phase: summary.gate?.phase ?? null,
    state: summary.gate?.state ?? result?.state ?? null,
    turn: summary.gate?.turn ?? latest?.n ?? null,
    result,
    verifier: latest?.verifier ?? null,
    proof: detail?.proof ?? null,
    fixTurns: fixTurnsOf(turns),
  }
}

/** Whether the task has anything gate-related to show. */
export function hasGate(view: GateView): boolean {
  return view.proof !== null || view.state !== null || view.phase !== null
}

/** Whether the panel should open by itself: the gate failed or the verifier rejected. */
export function gateUrgent(view: GateView): boolean {
  return (
    (view.phase === null && (view.state === 'red' || view.state === 'error')) ||
    view.verifier?.verdict === 'reject'
  )
}

export function gateTone(state: GateState | null, phase: GatePhase | null): Tone {
  if (phase) return 'running'
  switch (state) {
    case 'green':
      return 'success'
    case 'red':
    case 'error':
      return 'danger'
    case 'running':
      return 'running'
    default:
      return 'neutral'
  }
}

export function stepTone(state: StepState): Tone {
  switch (state) {
    case 'green':
      return 'success'
    case 'red':
    case 'error':
      return 'danger'
    case 'warn':
      return 'attention'
    case 'running':
      return 'running'
    case 'not_run':
      return 'muted'
    default:
      return 'neutral'
  }
}

/** Steps that did not pass (shown open), in gate order. */
export function failingSteps(result: GateResult | null): GateStep[] {
  return (result?.steps ?? []).filter((s) => s.state === 'red' || s.state === 'error')
}

/** The command line a step ran, with its folder when not the repository root. */
export function stepCommand(step: GateStep): string {
  if (!step.cmd) return ''
  return step.dir ? `cd ${step.dir} && ${step.cmd}` : step.cmd
}
