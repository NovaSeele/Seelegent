/**
 * What the Issues page shows for a ticket, an issue and the access tier: tones and icons (never colour alone),
 * the inbox order, and the small checks the forms make before the daemon does.
 */
import type { IconName } from '@/components/ui/icons'
import type { TaskStatus as DisplayStatus, Tone } from '@/components/ui/tone'
import type { CheckRun, Issue, TicketChecks, TicketSummary, TicketTask } from '@/api/types'

export type TicketStatus = TicketSummary['status']

/**
 * What a ticket shows: its status, or what the lead has to do when the daemon says so (needsAttention), which
 * says more than the status alone (a pr_open ticket whose branch waits for its pull request is not "PR open").
 */
export type TicketPhase = TicketStatus | NonNullable<TicketSummary['needsAttention']>

export function ticketPhase(t: Pick<TicketSummary, 'status' | 'needsAttention'>): TicketPhase {
  return t.needsAttention ?? t.status
}

/**
 * Attention (amber) means the lead or the user has to act: a task to review, a pull request to open, a refused
 * push, failed checks. No tier opens a pull request on its own, so a ready ticket waits for someone too. Danger is a
 * failure; muted is the normal end.
 */
export const TICKET_PHASE: Readonly<Record<TicketPhase, { tone: Tone; icon: IconName }>> = {
  working: { tone: 'running', icon: 'statusRunning' },
  review: { tone: 'attention', icon: 'statusReview' },
  ready: { tone: 'attention', icon: 'send' },
  pr_open: { tone: 'running', icon: 'git' },
  done: { tone: 'success', icon: 'statusAccepted' },
  dropped: { tone: 'muted', icon: 'statusCancelled' },
  failed: { tone: 'danger', icon: 'statusFailed' },
  push_refused: { tone: 'attention', icon: 'warning' },
  open_pr: { tone: 'attention', icon: 'external' },
  checks_failed: { tone: 'attention', icon: 'statusFailed' },
}

/** The i18n key of a phase's word: the status's own, or the next step's. */
export function phaseLabel(phase: TicketPhase): string {
  return phase === 'push_refused' || phase === 'open_pr' || phase === 'checks_failed'
    ? `issues.ticket.phase.${phase}`
    : `issues.ticket.status.${phase}`
}

/** The phases that wait for the lead or the user: listed first, under "Needs you". */
export function needsYou(phase: TicketPhase): boolean {
  return TICKET_PHASE[phase].tone === 'attention' || phase === 'failed'
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

/** One check's result in the words of CHECKS_STATE: running until it concludes, then passed or failed. */
export function runState(run: Pick<CheckRun, 'status' | 'conclusion'>): TicketChecks['state'] {
  if (!run.conclusion) return run.status === 'completed' ? 'unknown' : 'pending'
  if (FAILING.has(run.conclusion)) return 'failure'
  return run.conclusion === 'success' ? 'success' : 'unknown'
}

/**
 * The number of a pull request from what the user typed: "43", "#43" or its address (…/pull/43, with or without
 * a trailing part). null when there is none.
 */
export function prNumber(text: string): number | null {
  const s = text.trim()
  const m = /^#?(\d+)$/.exec(s) ?? /\/pull\/(\d+)(?:[/?#].*)?$/.exec(s)
  const n = m ? Number(m[1]) : NaN
  return Number.isSafeInteger(n) && n > 0 ? n : null
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

/**
 * True when a failed pr call was a refused push (a 409 whose details carry one of the refusal reasons): the daemon
 * keeps the refusal on the ticket, and the ticket's banner explains it in the reader's language.
 */
export function isPushRefusal(details: Readonly<Record<string, unknown>>): boolean {
  return (
    details.reason === 'unsafe_files' ||
    details.reason === 'scan_failed' ||
    details.reason === 'base_not_pushed' ||
    details.reason === 'branch_diverged'
  )
}
