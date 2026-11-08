/**
 * The accounts that wait for the user, one problem each, with the step that fixes it: no key (add one), signed out
 * (sign in), a key the service refused or a wrong address (replace the key), no credit left (top up, then test the
 * key; a sign-in account is switched off and on instead, which the hint says) and a budget reached (raise it). A
 * plan that cannot use its backend has no step here. Accounts switched off or signing in are left out: the user
 * already chose, or is acting.
 */
import type { Account } from '@/api/types'

export type NeedProblem =
  'no_key' | 'needs_login' | 'refused' | 'credits' | 'budget_reached' | 'no_plan'
export type NeedAction = 'key' | 'login' | 'replaceKey' | 'test' | 'budget'

export interface Need {
  account: Account
  problem: NeedProblem
  action: NeedAction | null
}

/** Test results that say the key or the address is wrong (not a passing service being slow or down). */
export const REFUSED_TESTS: ReadonlySet<NonNullable<Account['testResult']>> = new Set([
  'refused',
  'not_found',
  'redirected',
])

function need(a: Account): Need | null {
  const apiKey = a.kind === 'api-key'
  if (a.status === 'no_key') return { account: a, problem: 'no_key', action: 'key' }
  if (a.status === 'needs_login' && !apiKey)
    return { account: a, problem: 'needs_login', action: 'login' }
  if (a.status === 'no_plan') return { account: a, problem: 'no_plan', action: null }
  if (apiKey && a.testResult && REFUSED_TESTS.has(a.testResult))
    return { account: a, problem: 'refused', action: 'replaceKey' }
  if (a.restReason === 'credits')
    return { account: a, problem: 'credits', action: apiKey ? 'test' : null }
  if (a.restReason === 'budget_reached')
    return { account: a, problem: 'budget_reached', action: 'budget' }
  return null
}

export function needsYou(accounts: readonly Account[]): Need[] {
  return accounts.flatMap((a) => {
    if (!a.enabled || a.signingIn) return []
    const found = need(a)
    return found ? [found] : []
  })
}
