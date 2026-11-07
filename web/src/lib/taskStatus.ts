/**
 * The one place that derives what a task means for the person watching: its display status (word, icon and tone
 * from components/ui/tone.ts), whether it needs someone, and whether it is an internal helper run that lists and
 * counts leave out. Every view reads these; none re-derives them.
 */
import type { TaskStatus as ApiStatus, TaskSummary } from '@/api/types'
import type { TaskStatus as DisplayStatus } from '@/components/ui/tone'

export type { DisplayStatus }

export const ACTIVE_STATUSES: readonly ApiStatus[] = ['queued', 'running', 'waiting_quota']
export const PENDING_STATUSES: readonly ApiStatus[] = [...ACTIVE_STATUSES, 'waiting_deps']
export const FINISHED_STATUSES: readonly ApiStatus[] = ['done', 'failed', 'cancelled']

/** Helper runs the engine starts for itself (commit messages, consults, judges, meeting opinions). */
export const HIDDEN_KINDS: ReadonlySet<string> = new Set([
  'commit-msg',
  'consult',
  'judge',
  'meeting',
])

/** Attention states that mean "someone must act"; background_wait and info are only shown, not counted. */
const BLOCKING_ATTENTION: ReadonlySet<string> = new Set(['blocked', 'question', 'stalled'])

export const isActive = (task: Pick<TaskSummary, 'status'>) => ACTIVE_STATUSES.includes(task.status)
export const isPending = (task: Pick<TaskSummary, 'status'>) =>
  PENDING_STATUSES.includes(task.status)
export const isFinished = (task: Pick<TaskSummary, 'status'>) =>
  FINISHED_STATUSES.includes(task.status)
export const isVisibleTask = (task: Pick<TaskSummary, 'kind'>) => !HIDDEN_KINDS.has(task.kind)

export function isBlocked(task: Pick<TaskSummary, 'attention'>): boolean {
  return !!task.attention && BLOCKING_ATTENTION.has(task.attention.state)
}

export function awaitsReview(task: Pick<TaskSummary, 'status' | 'review'>): boolean {
  return task.status === 'done' && !task.review
}

export function displayStatus(task: TaskSummary): DisplayStatus {
  if (isBlocked(task) && !isFinished(task)) return 'blocked'
  switch (task.status) {
    case 'queued':
      return 'queued'
    case 'running':
      return 'running'
    case 'waiting_quota':
      return 'waiting'
    case 'waiting_deps':
      return 'waitingDeps'
    case 'blocked_deps':
      return 'blockedDeps'
    case 'done':
      if (task.review?.verdict === 'accepted') return 'accepted'
      if (task.review?.verdict === 'rejected') return 'rejected'
      return isBlocked(task) ? 'blocked' : 'review'
    case 'failed':
      return 'failed'
    case 'cancelled':
      return 'cancelled'
  }
}

/**
 * The i18n key of a task's status word. A blocked task says why (a question, stalled) so the pill, the list,
 * the banner and the Watch board use one name per state.
 */
export function statusWordKey(task: TaskSummary): string {
  const status = displayStatus(task)
  if (status === 'blocked') {
    const state = task.attention?.state
    if (state === 'question') return 'common.attention.question'
    if (state === 'stalled') return 'common.attention.stalled'
  }
  return `common.status.${status}.label`
}

/** Why a task is on the "needs you" board, most urgent first. */
export type NeedKind = 'question' | 'blocked' | 'stalled' | 'failed' | 'depsBlocked' | 'review'

export const NEED_ORDER: readonly NeedKind[] = [
  'question',
  'blocked',
  'stalled',
  'failed',
  'depsBlocked',
  'review',
]

/** The word for each reason on the board: the same words the status pill and the banner use. */
export const NEED_WORD_KEY: Readonly<Record<NeedKind, string>> = {
  question: 'common.attention.question',
  blocked: 'common.status.blocked.label',
  stalled: 'common.attention.stalled',
  failed: 'common.status.failed.label',
  depsBlocked: 'common.status.blockedDeps.label',
  review: 'common.status.review.label',
}

/** Failed tasks stay on the board this long; after that they are history. */
export const FAILED_NEEDS_MS = 24 * 3600_000

export function needKind(task: TaskSummary, now = Date.now()): NeedKind | null {
  if (!isVisibleTask(task)) return null
  if (task.attention && BLOCKING_ATTENTION.has(task.attention.state) && !isFinished(task)) {
    return task.attention.state as 'question' | 'blocked' | 'stalled'
  }
  if (task.status === 'done' && task.attention?.state === 'question') return 'question'
  if (task.status === 'failed' && !task.review) {
    return now - Date.parse(task.updatedAt) < FAILED_NEEDS_MS ? 'failed' : null
  }
  if (task.status === 'blocked_deps') return 'depsBlocked'
  if (awaitsReview(task)) return 'review'
  return null
}

/** Since when the task has been waiting: the daemon's needsSince, else the attention or last update time. */
export function waitingSince(task: TaskSummary): string {
  return task.needsSince ?? task.attention?.since ?? task.updatedAt
}

/** Who can act on a task right now. */
export function canFollowup(task: TaskSummary): boolean {
  return !isActive(task) && !task.manual
}

export function canCancel(task: TaskSummary): boolean {
  return isPending(task) || task.status === 'blocked_deps'
}

export function canReview(task: TaskSummary): boolean {
  return task.status === 'done'
}

export function canTakeOver(task: TaskSummary): boolean {
  return !!task.accountId && !task.manual && task.status !== 'cancelled'
}
