/**
 * How the Accounts page names and colours an account. The status words follow contract §4 (ok → Ready, new →
 * Checking quota, error → Ready with "last read failed", needs_login → Sign-in required, no_key → No API key,
 * no_plan → Plan cannot use this backend, twin → Same sign-in as …, disabled → Off); runtime facts (busy, resting,
 * out of quota, signing in) are separate chips, so the status never hides them.
 */
import { shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import type { Account, AccountStatus } from '@/api/types'
import type { Tone } from '@/components/ui/tone'
import { useToastStore } from '@/stores/toast'

export const BACKENDS = ['agy', 'claude', 'codex'] as const satisfies readonly Account['backend'][]

export const STATUS_TONE: Readonly<Record<AccountStatus, Tone>> = {
  ok: 'success',
  error: 'success',
  new: 'neutral',
  needs_login: 'danger',
  no_key: 'danger',
  no_plan: 'danger',
  twin: 'muted',
  disabled: 'muted',
}

/** The i18n key of a kind (catalog keys have no hyphen). */
export const kindKey = (kind: Account['kind']): 'isolated' | 'system' | 'apiKey' =>
  kind === 'api-key' ? 'apiKey' : kind

/** Can this account sign in (isolated and system accounts; API-key accounts use a key)? */
export const canSignIn = (account: Account): boolean => account.kind !== 'api-key'

/** Is the account out of quota right now? */
export const isExhausted = (account: Account, now: number): boolean =>
  !!account.quota.exhaustedUntil && Date.parse(account.quota.exhaustedUntil) > now

/**
 * Can the account take a new turn now: on, signed in (or with old quota numbers), not resting, not out of quota,
 * and with a free slot (a 1/1 busy account makes a new delegation wait, A-L16).
 */
export const readyForWork = (account: Account, now: number): boolean =>
  account.enabled &&
  (account.status === 'ok' || account.status === 'error') &&
  !account.resting &&
  !isExhausted(account, now) &&
  account.busy < account.maxConcurrent

/**
 * What the card's pill shows. A Ready account (ok, or error with old numbers) that is held until the user acts (no
 * credit left, budget reached), out of quota or resting shows that instead of Ready (attention tone for a hold and
 * for out of quota, neutral for resting), and the matching chip is left out; every other status keeps its own word.
 */
export type PillState = 'status' | 'held' | 'exhausted' | 'resting'

export function pillState(account: Account, now: number): PillState {
  if (account.status !== 'ok' && account.status !== 'error') return 'status'
  if (account.restReason) return 'held'
  if (isExhausted(account, now)) return 'exhausted'
  if (account.resting) return 'resting'
  return 'status'
}

const CANNOT_RUN: ReadonlySet<AccountStatus> = new Set([
  'disabled',
  'no_key',
  'no_plan',
  'needs_login',
  'twin',
])

/**
 * Is the "busy of max slots" chip worth showing? Not for an account that cannot take work (off, no key, no plan,
 * signed out, or a second sign-in whose twin gets the work), unless a run is still going on it.
 */
export const showsSlots = (account: Account): boolean =>
  account.busy > 0 || (account.enabled && !CANNOT_RUN.has(account.status))

/** What turning an account off changes now: the turns running on it, and whether no other account takes its work. */
export interface OffConsequence {
  running: number
  only: boolean
}

/**
 * What turning `account` off changes now, or null when the switch can flip without asking: no turn runs on it and
 * another account of the same backend and provider is on and can take work now. Accounts that cannot take work
 * (off, signed out, no key, no plan, a second sign-in) or cannot take it now (out of quota, resting) do not count as
 * that other account.
 */
export function offConsequence(
  account: Account,
  all: readonly Account[],
  now: number,
): OffConsequence | null {
  const provider = account.provider ?? null
  // An account that cannot take work itself (signed out, no key, …) leaves nothing behind when it goes off.
  const only =
    !CANNOT_RUN.has(account.status) &&
    !all.some(
      (a) =>
        a.id !== account.id &&
        a.backend === account.backend &&
        (a.provider ?? null) === provider &&
        a.enabled &&
        !CANNOT_RUN.has(a.status) &&
        !a.resting &&
        !isExhausted(a, now),
    )
  if (account.busy === 0 && !only) return null
  return { running: account.busy, only }
}

export const PILL_TONE: Readonly<Record<Exclude<PillState, 'status'>, Tone>> = {
  held: 'attention',
  exhausted: 'attention',
  resting: 'neutral',
}

/**
 * Runs page actions one at a time per key with a busy marker, and shows a failure as an error toast (the daemon's
 * own message). Returns whether the action worked.
 */
export function useActions() {
  const { t } = useI18n()
  const toast = useToastStore()
  const busy = shallowRef<ReadonlySet<string>>(new Set())

  async function run(key: string, action: () => Promise<unknown>, done?: string): Promise<boolean> {
    if (busy.value.has(key)) return false
    busy.value = new Set([...busy.value, key])
    try {
      await action()
      if (done) toast.show({ message: done, tone: 'success' })
      return true
    } catch (error) {
      toast.show({ message: t('accounts.failed', { error: errorMessage(error) }), tone: 'error' })
      return false
    } finally {
      const next = new Set(busy.value)
      next.delete(key)
      busy.value = next
    }
  }

  return { busy, run, isBusy: (key: string) => busy.value.has(key) }
}
