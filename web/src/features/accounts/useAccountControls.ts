import { computed, toValue, type MaybeRefOrGetter } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Account } from '@/api/types'
import type { MenuItem } from '@/components/ui/Menu.vue'
import { useAccountsStore } from '@/stores/accounts'

import { canSignIn, offConsequence, useActions } from './accountView'

export type AccountDialog = 'rename' | 'key' | 'delete' | 'logout' | 'off' | 'priority'

/**
 * The controls an account card or list row shares: its type line, its running sign-in, the next step its status
 * asks for (sign in, set a key), the ⋯ menu, and the on/off and priority changes. Dialogs belong to the page;
 * `dialog` asks for one. `priorityInMenu` adds "Priority…" to the menu, for a narrow row that has no room for the
 * field.
 */
export function useAccountControls(
  props: { readonly account: Account; readonly now: number },
  dialog: (kind: AccountDialog) => void,
  priorityInMenu: MaybeRefOrGetter<boolean> = false,
) {
  const { t } = useI18n()
  const accounts = useAccountsStore()
  const actions = useActions()

  const session = computed(() => accounts.logins[props.account.id])
  const kindLabel = computed(() =>
    props.account.kind === 'api-key'
      ? t('accounts.kind.apiKeyOf', { provider: props.account.provider ?? '?' })
      : t(`accounts.kind.${props.account.kind}`),
  )

  const next = computed<'login' | 'key' | null>(() => {
    const a = props.account
    if (session.value && session.value.state !== 'done' && session.value.state !== 'failed')
      return null
    if (a.status === 'needs_login' && canSignIn(a)) return 'login'
    if (a.status === 'no_key') return 'key'
    return null
  })

  const menu = computed<MenuItem[]>(() => {
    const a = props.account
    const items: MenuItem[] = [
      { id: 'refresh', label: t('accounts.action.refresh'), icon: 'refresh' },
      { id: 'rename', label: t('accounts.action.rename'), icon: 'accounts' },
    ]
    if (toValue(priorityInMenu)) {
      items.push({
        id: 'priority',
        label: t('accounts.action.priority', { n: a.priority }),
        icon: 'settings',
      })
    }
    if (canSignIn(a)) {
      items.push({ id: 'login', label: t('accounts.action.login'), icon: 'external' })
      if (a.kind === 'isolated' && a.status !== 'needs_login') {
        items.push({ id: 'logout', label: t('accounts.action.logout'), icon: 'arrowLeft' })
      }
    } else items.push({ id: 'key', label: t('accounts.action.key'), icon: 'settings' })
    items.push({
      id: 'delete',
      label: t('accounts.action.delete'),
      icon: 'trash',
      danger: true,
      separator: true,
    })
    return items
  })

  const DIALOGS: ReadonlySet<string> = new Set(['rename', 'key', 'delete', 'logout', 'priority'])

  function onMenu(item: string) {
    const a = props.account
    if (item === 'refresh') void actions.run('refresh', () => accounts.refresh(a.id))
    else if (item === 'login') void actions.run('login', () => accounts.login(a.id))
    else if (DIALOGS.has(item)) dialog(item as AccountDialog)
  }

  // Turning off asks first only when it changes something now: turns run on it, or nothing else takes its work.
  function setEnabled(enabled: boolean) {
    if (!enabled && offConsequence(props.account, accounts.accounts, props.now)) {
      dialog('off')
      return
    }
    void actions.run('enabled', () => accounts.update(props.account.id, { enabled }))
  }

  function setPriority(priority: number) {
    void actions.run('priority', () => accounts.update(props.account.id, { priority }))
  }

  return { actions, session, kindLabel, next, menu, onMenu, setEnabled, setPriority }
}
