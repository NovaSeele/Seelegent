/** Typed endpoints of accounts, sign-in and quota (contract §4). Emails never go into a URL or a log. */
import type { ApiClient } from './client'
import type { Account, AccountPatch, LoginSession, NewAccount } from './types'

export type { NewAccount }

/** PATCH /accounts/{id}: absent fields stay as they are. */
export type AccountChanges = AccountPatch

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
