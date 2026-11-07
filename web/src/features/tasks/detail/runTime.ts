/** How long a task's worker ran, for the task header. */
import type { Turn } from '@/api/types'

/**
 * The milliseconds the worker spent on its turns: each turn from its start to its end (or `now` while it runs).
 * Waiting for the lead, for a review or for an account is not run time. Null without turns (the header then
 * falls back to the task's age).
 */
export function runTime(
  turns: readonly Pick<Turn, 'startedAt' | 'endedAt'>[],
  now: number,
): number | null {
  let total = 0
  let counted = false
  for (const turn of turns) {
    const start = Date.parse(turn.startedAt)
    if (Number.isNaN(start)) continue
    const end = turn.endedAt ? Date.parse(turn.endedAt) : now
    if (Number.isNaN(end)) continue
    total += Math.max(0, end - start)
    counted = true
  }
  return counted ? total : null
}
