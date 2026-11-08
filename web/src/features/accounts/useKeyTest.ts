import { useI18n } from 'vue-i18n'

import type { Account } from '@/api/types'
import { useAccountsStore } from '@/stores/accounts'
import { useProvidersStore } from '@/stores/providers'
import { useToastStore } from '@/stores/toast'

import { useActions } from './accountView'
import { keyTestMessage, type Translate } from './keyTest'

/**
 * "Test again" on an API-key account: tests its stored key against its own service (the result is saved on the
 * account), and says the result in a toast: green for a pass, neutral for a service that cannot be checked, red
 * otherwise. A pass while the account had no credit says the test cannot see the balance.
 */
export function useKeyTest() {
  const { t } = useI18n()
  const accounts = useAccountsStore()
  const providers = useProvidersStore()
  const toast = useToastStore()
  const actions = useActions()

  function test(account: Account): Promise<boolean> {
    return actions.run(`test:${account.id}`, async () => {
      const result = await providers.test({ accountId: account.id })
      accounts.noteTest(account.id, result)
      toast.show({
        message: `${account.label}: ${keyTestMessage(result, t as Translate)}`,
        tone: result.ok ? 'success' : result.reason === 'no_models_endpoint' ? 'info' : 'error',
      })
    })
  }

  return { test, isTesting: (id: string) => actions.isBusy(`test:${id}`) }
}
