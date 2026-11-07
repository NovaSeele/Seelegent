/** Pure helpers of the task's member opinions (consults): which objections stand and how each verdict reads. */
import type { Consult, TaskDetail } from '@/api/types'
import type { Tone } from '@/components/ui/tone'

/** A peer objection the lead has not answered: it blocks review accepted (contract: consult). */
export function isOpenObjection(c: Consult): boolean {
  return c.rank === 'peer' && c.status === 'done' && c.verdict === 'object' && !c.answeredObjection
}

export function openObjections(detail: TaskDetail | null | undefined): Consult[] {
  return (detail?.consults ?? []).filter(isOpenObjection)
}

/**
 * The verdict's tone: only an objection the lead must still answer asks for attention; an answered one is
 * history (muted), concerns are information (neutral), a failed run is a failure.
 */
export function consultTone(c: Consult): Tone {
  if (c.status === 'running') return 'running'
  if (c.status === 'failed') return 'danger'
  if (c.verdict === 'object') {
    if (isOpenObjection(c)) return 'attention'
    // A senior's objection is a warning for the lead to weigh, not a step it must take.
    return c.answeredObjection ? 'muted' : 'neutral'
  }
  if (c.verdict === 'approve') return 'success'
  return 'neutral'
}

/** The verdict's i18n key under task.consults.verdict. */
export function consultState(c: Consult): string {
  if (c.status !== 'done') return c.status
  if (c.verdict === 'object' && c.answeredObjection) return 'answered'
  return c.verdict ?? 'done'
}

export { memberLabel } from '@/features/members/memberModel'
