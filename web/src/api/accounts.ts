/** The account reads the task views need (contract §4). The Accounts page adds the writes. */
import type { ApiClient } from './client'
import type { Account } from './types'

export function createAccountsApi(client: ApiClient) {
  return {
    list: (signal?: AbortSignal) => client.get<Account[]>('/accounts', { signal }),
  }
}

export type AccountsApi = ReturnType<typeof createAccountsApi>
