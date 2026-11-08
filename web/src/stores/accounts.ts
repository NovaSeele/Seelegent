/**
 * Worker accounts: labels, state and quota from GET /state and SSE account.updated/deleted, ordered like
 * GET /accounts (priority descending, then label), plus the Accounts page's writes and sign-in sessions.
 *
 * Writes upsert the account the daemon returns, so the page does not wait for the SSE frame. Sign-in progress
 * comes from account.login frames (`applyLogin`); a finished session (done or failed) stays until the page
 * clears it, so its outcome can be shown.
 */
import { defineStore } from 'pinia'
import { computed, shallowRef } from 'vue'

import { accountsApi as defaultApi } from '@/api'
import type { AccountChanges, AccountsApi, NewAccount } from '@/api/accounts'
import type { Account, AccountCreated, KeyTestResult, LoginSession } from '@/api/types'

import { useProvidersStore } from './providers'

function order(list: readonly Account[]): Account[] {
  return [...list].sort((a, b) => b.priority - a.priority || a.label.localeCompare(b.label))
}

export const useAccountsStore = defineStore('accounts', () => {
  let client: AccountsApi = defaultApi
  const accounts = shallowRef<readonly Account[]>([])
  const byId = computed(() => new Map(accounts.value.map((account) => [account.id, account])))
  /** The latest sign-in session per account id. */
  const logins = shallowRef<Readonly<Record<string, LoginSession>>>({})

  function setApi(next: AccountsApi): void {
    client = next
  }

  function replaceAll(list: readonly Account[]): void {
    accounts.value = order(list)
  }

  function upsert(account: Account): void {
    accounts.value = order([...accounts.value.filter((a) => a.id !== account.id), account])
  }

  /** Upserts unless the stored account is as new or newer (by updatedAt); for replays that may be stale. */
  function upsertIfNewer(account: Account): void {
    const current = byId.value.get(account.id)
    if (current && Date.parse(current.updatedAt) >= Date.parse(account.updatedAt)) return
    upsert(account)
  }

  function remove(id: string): void {
    accounts.value = accounts.value.filter((a) => a.id !== id)
    clearLogin(id)
  }

  function get(id: string | undefined): Account | undefined {
    return id ? byId.value.get(id) : undefined
  }

  function applyLogin(session: LoginSession): void {
    logins.value = { ...logins.value, [session.accountId]: session }
  }

  function clearLogin(id: string): void {
    if (!(id in logins.value)) return
    const next = { ...logins.value }
    delete next[id]
    logins.value = next
  }

  async function keep<T extends Account>(call: Promise<T>): Promise<T> {
    const account = await call
    upsert(account)
    return account
  }

  async function keepAll(call: Promise<Account[]>): Promise<void> {
    for (const account of await call) upsertIfNewer(account)
  }

  /**
   * Adds an account in one call (POST /accounts): for an API key the provider (existing, or created or reused
   * from newProvider) and the key go with it, and a refused key leaves nothing behind. A provider the call
   * created goes into the providers store at once.
   */
  async function create(body: NewAccount): Promise<AccountCreated> {
    const created = await client.create(body)
    // The list keeps the account alone: the provider and the notices belong to this answer.
    const account: AccountCreated = { ...created }
    delete account.newProvider
    delete account.providerCreated
    delete account.notices
    upsert(account)
    if (created.newProvider) useProvidersStore().upsert(created.newProvider)
    return created
  }

  /**
   * Notes a key test that was saved on the account (POST /providers/test with accountId), so its state shows
   * before the account.updated frame arrives; the frame then brings the daemon's own record.
   */
  function noteTest(id: string, result: KeyTestResult): void {
    const account = byId.value.get(id)
    if (!account || !result.saved) return
    accounts.value = order([
      ...accounts.value.filter((a) => a.id !== id),
      { ...account, testResult: result.reason, testedAt: new Date().toISOString() },
    ])
  }

  async function removeAccount(id: string): Promise<void> {
    await client.remove(id)
    remove(id)
  }

  /** Starts a sign-in; frames carry the rest. */
  async function login(id: string): Promise<LoginSession> {
    const session = await client.login(id)
    applyLogin(session)
    return session
  }

  /** Reads a running sign-in again (after a reload); clears it when none is running. */
  async function resumeLogin(id: string): Promise<void> {
    try {
      applyLogin(await client.loginState(id))
    } catch {
      clearLogin(id)
    }
  }

  async function sendCode(id: string, code: string): Promise<void> {
    applyLogin(await client.loginCode(id, code))
  }

  return {
    accounts,
    byId,
    logins,
    setApi,
    replaceAll,
    upsert,
    upsertIfNewer,
    remove,
    get,
    applyLogin,
    clearLogin,
    create,
    noteTest,
    removeAccount,
    update: (id: string, changes: AccountChanges) => keep(client.update(id, changes)),
    refresh: (id: string) => keep(client.refresh(id)),
    refreshAll: () => keepAll(client.refreshAll()),
    login,
    resumeLogin,
    sendCode,
    logout: (id: string) => keep(client.logout(id)),
    setKey: (id: string, key: string) => keep(client.setKey(id, key)),
    clearKey: (id: string) => keep(client.clearKey(id)),
  }
})
