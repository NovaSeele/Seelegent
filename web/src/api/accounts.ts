/** Typed endpoints of accounts, sign-in and quota (contract §4). Emails never go into a URL or a log. */
import type { ApiClient } from './client'
import type { Account, LoginSession } from './types'

export interface NewAccount {
  backend: Account['backend']
  kind: Account['kind']
  label: string
  /** Required for kind api-key, absent otherwise. */
  provider?: string
  enabled?: boolean
  priority?: number
}

export type AccountChanges = Partial<Pick<Account, 'label' | 'enabled' | 'priority'>>

const enc = encodeURIComponent

export function createAccountsApi(client: ApiClient) {
  const path = (id: string, sub = '') => `/accounts/${enc(id)}${sub}`
  return {
    /** Priority descending, then label. */
    list: (signal?: AbortSignal) => client.get<Account[]>('/accounts', { signal }),
    create: (body: NewAccount) => client.post<Account>('/accounts', { body }),
    update: (id: string, body: AccountChanges) => client.patch<Account>(path(id), { body }),
    /** 409 bad_state while the account runs a task. */
    remove: (id: string) => client.delete<void>(path(id)),
    refresh: (id: string) => client.post<Account>(path(id, '/refresh'), { body: {} }),
    refreshAll: () => client.post<Account[]>('/quota/refresh', { body: {} }),
    login: (id: string) => client.post<LoginSession>(path(id, '/login'), { body: {} }),
    /** 404 when no sign-in is running. */
    loginState: (id: string) => client.get<LoginSession>(path(id, '/login')),
    loginCode: (id: string, code: string) =>
      client.post<LoginSession>(path(id, '/login/code'), { body: { code } }),
    logout: (id: string) => client.post<Account>(path(id, '/logout'), { body: {} }),
    setKey: (id: string, key: string) => client.put<Account>(path(id, '/key'), { body: { key } }),
    clearKey: (id: string) => client.delete<Account>(path(id, '/key')),
  }
}

export type AccountsApi = ReturnType<typeof createAccountsApi>
