/**
 * What the Issues page shows for a ticket, an issue and the access tier: tones and icons (never colour alone),
 * the inbox order, and the small checks the forms make before the daemon does.
 */
import type { IconName } from '@/components/ui/icons'
import type { TaskStatus as DisplayStatus, Tone } from '@/components/ui/tone'
import type { Issue, TicketChecks, TicketSummary, TicketTask } from '@/api/types'

export type TicketStatus = TicketSummary['status']

export const TICKET_STATUS: Readonly<Record<TicketStatus, { tone: Tone; icon: IconName }>> = {
  working: { tone: 'running', icon: 'statusRunning' },
  review: { tone: 'attention', icon: 'statusReview' },
  ready: { tone: 'accent', icon: 'statusWaiting' },
  pr_open: { tone: 'running', icon: 'git' },
  done: { tone: 'success', icon: 'statusAccepted' },
  dropped: { tone: 'muted', icon: 'statusCancelled' },
  failed: { tone: 'danger', icon: 'statusFailed' },
}

export const CHECKS_STATE: Readonly<Record<TicketChecks['state'], { tone: Tone; icon: IconName }>> =
  {
    pending: { tone: 'running', icon: 'statusRunning' },
    success: { tone: 'success', icon: 'statusAccepted' },
    failure: { tone: 'danger', icon: 'statusFailed' },
    unknown: { tone: 'muted', icon: 'question' },
  }

export const TIER_TONE: Readonly<Record<string, Tone>> = {
  gh: 'success',
  token: 'success',
  manual: 'attention',
}

/** Conclusions that count as a failed check (as the daemon judges them). */
const FAILING: ReadonlySet<string> = new Set(['failure', 'timed_out', 'startup_failure', 'error'])

/** The failing check names of a ticket's checks. */
export function failingChecks(checks: TicketChecks | null | undefined): string[] {
  return (checks?.runs ?? [])
    .filter((r) => r.conclusion !== undefined && FAILING.has(r.conclusion))
    .map((r) => r.name)
}

/**
 * The inbox order: issues that still need a decision first, then the rest; newest change first in each group.
 */
export function inboxOrder(list: readonly Issue[]): Issue[] {
  const rank = (i: Issue) => (i.state === 'open' && !i.decision ? 0 : 1)
  return [...list].sort(
    (a, b) => rank(a) - rank(b) || (b.updatedAt || '').localeCompare(a.updatedAt || ''),
  )
}

/** True for an absolute folder (Windows drive or UNC, or POSIX). */
export function isAbsolutePath(p: string): boolean {
  return /^([a-zA-Z]:[\\/]|[\\/])/.test(p.trim())
}

/** The trimmed, non-empty lines of a text field. */
export function lines(text: string): string[] {
  return text
    .split(/\r?\n/)
    .map((s) => s.trim())
    .filter(Boolean)
}

/** Only an http(s) link may become an anchor (the daemon already checks; this is the page's own guard). */
export function safeUrl(url: string | undefined): string | undefined {
  return url && /^https?:\/\//i.test(url) ? url : undefined
}

/** A finished ticket (done, dropped or failed) gets no new task; only its worktree can be cleaned up. */
export function ticketFinished(status: TicketStatus): boolean {
  return status === 'done' || status === 'dropped' || status === 'failed'
}

/** A ticket whose branch can be pushed and get a pull request (or have one linked). */
export function ticketPushable(status: TicketStatus): boolean {
  return status === 'ready' || status === 'pr_open'
}

/** A ticket that can still be dropped (a failed one can). */
export function ticketDroppable(status: TicketStatus): boolean {
  return status !== 'done' && status !== 'dropped'
}

/** The display status of a ticket's task (its word and pill come from common.status.<key>). */
export function ticketTaskStatus(task: Pick<TicketTask, 'status' | 'verdict'>): DisplayStatus {
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
      if (task.verdict === 'accepted') return 'accepted'
      if (task.verdict === 'rejected') return 'rejected'
      return 'review'
    case 'failed':
      return 'failed'
    case 'cancelled':
      return 'cancelled'
  }
}

/** A short one-line text of a ticket log step's data (the daemon has already masked tokens in it). */
export function logText(data: Record<string, unknown>, limit = 240): string {
  const parts: string[] = []
  for (const [k, v] of Object.entries(data)) {
    if (v === null || v === undefined || v === '') continue
    parts.push(`${k}: ${typeof v === 'string' ? v : JSON.stringify(v)}`)
  }
  const text = parts.join(' · ')
  return text.length > limit ? `${text.slice(0, limit - 1)}…` : text
}
