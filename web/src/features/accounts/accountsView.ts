/**
 * Which view the Accounts page shows: the compact list (default) or the cards. The choice is a per-viewer
 * preference in localStorage; lib/storage wraps every read and write, so the page works with storage blocked
 * (the choice then lasts until reload).
 */
import { ref, watch, type Ref } from 'vue'

import { isOneOf, readStored, writeStored, type StoredValue } from '@/lib/storage'

export const ACCOUNT_VIEWS = ['list', 'cards'] as const
export type AccountsView = (typeof ACCOUNT_VIEWS)[number]

export const ACCOUNTS_VIEW: StoredValue<AccountsView> = {
  name: 'accounts.view',
  fallback: 'list',
  validate: isOneOf(ACCOUNT_VIEWS),
}

export function useAccountsView(): Ref<AccountsView> {
  const view = ref<AccountsView>(readStored(ACCOUNTS_VIEW))
  watch(view, (value) => writeStored(ACCOUNTS_VIEW, value))
  return view
}
