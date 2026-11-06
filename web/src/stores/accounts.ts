/**
 * Worker accounts as the task views need them (labels, state, quota), from GET /state and SSE
 * account.updated/deleted. Ordered like GET /accounts: priority descending, then label. The Accounts page
 * (milestone 11) adds the writes.
 */
import { defineStore } from 'pinia'
import { computed, shallowRef } from 'vue'

import type { Account } from '@/api/types'

function order(list: readonly Account[]): Account[] {
  return [...list].sort((a, b) => b.priority - a.priority || a.label.localeCompare(b.label))
}

export const useAccountsStore = defineStore('accounts', () => {
  const accounts = shallowRef<readonly Account[]>([])
  const byId = computed(() => new Map(accounts.value.map((account) => [account.id, account])))

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
  }

  function get(id: string | undefined): Account | undefined {
    return id ? byId.value.get(id) : undefined
  }

  return { accounts, byId, replaceAll, upsert, upsertIfNewer, remove, get }
})
