/**
 * What an account means at a glance: one display state (from the pool's status plus its runtime: signing in,
 * out of quota, resting, busy) with its tone, and its tightest quota window. Views read these instead of
 * re-deriving them from Account fields.
 */
import type { Account, QuotaWindow } from '@/api/types'
import type { Tone } from '@/components/ui/tone'

export type AccountState =
  | 'ready'
  | 'busy'
  | 'resting'
  | 'exhausted'
  | 'checking'
  | 'signingIn'
  | 'needsLogin'
  | 'noKey'
  | 'noPlan'
  | 'twin'
  | 'disabled'

export const ACCOUNT_TONE: Readonly<Record<AccountState, Tone>> = {
  ready: 'success',
  busy: 'running',
  resting: 'neutral',
  exhausted: 'attention',
  checking: 'neutral',
  signingIn: 'neutral',
  needsLogin: 'danger',
  noKey: 'danger',
  noPlan: 'danger',
  twin: 'muted',
  disabled: 'muted',
}

export function accountState(account: Account, now = Date.now()): AccountState {
  switch (account.status) {
    case 'disabled':
      return 'disabled'
    case 'twin':
      return 'twin'
    case 'no_key':
      return 'noKey'
    case 'no_plan':
      return 'noPlan'
    case 'needs_login':
      return account.signingIn ? 'signingIn' : 'needsLogin'
    default:
      break
  }
  if (account.signingIn) return 'signingIn'
  if (account.status === 'new') return 'checking'
  const until = account.quota.exhaustedUntil
  if (until && Date.parse(until) > now) return 'exhausted'
  if (account.resting) return 'resting'
  // ok and error (last read failed, old numbers) are both usable.
  return account.busy > 0 ? 'busy' : 'ready'
}

export interface TightWindow extends QuotaWindow {
  group: string
}

/** The window with the least left across every group; undefined when the quota was never read. */
export function tightestWindow(account: Account): TightWindow | undefined {
  let best: TightWindow | undefined
  for (const group of account.quota.groups) {
    for (const window of group.windows) {
      if (!best || window.remaining < best.remaining) best = { ...window, group: group.name }
    }
  }
  return best
}

/** "gemini-pro · 5h" style name of a window. */
export function windowName(window: TightWindow): string {
  return `${window.group} · ${window.label || window.window}`
}
