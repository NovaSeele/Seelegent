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
