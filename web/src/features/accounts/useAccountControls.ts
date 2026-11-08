import { computed, toValue, type MaybeRefOrGetter } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Account } from '@/api/types'
import type { MenuItem } from '@/components/ui/Menu.vue'
import { useProviderNames } from '@/composables/useProviderNames'
import { useAccountsStore } from '@/stores/accounts'

import { canSignIn, offConsequence, useActions } from './accountView'
import { useKeyTest } from './useKeyTest'

export type AccountDialog =
  'rename' | 'key' | 'delete' | 'logout' | 'off' | 'priority' | 'budget' | 'model'

/**
 * The controls an account card or list row shares: its type line ("Subscription · Own sign-in", "API key ·
 * Anthropic"), its running sign-in, the next step its status asks for (sign in, add a key), the ⋯ menu, and the
 * on/off and priority changes. An API key's menu has Test again, Replace key, Change model, Set budget, Rename,
 * Turn off and Remove; a sign-in account's has quota refresh, sign-in and sign-out. Dialogs belong to the page;
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
  const providerName = useProviderNames(() => [props.account.provider])
  const actions = useActions()
  const keyTest = useKeyTest()

  const session = computed(() => accounts.logins[props.account.id])
  const kindLabel = computed(() =>
    props.account.kind === 'api-key'
      ? t('accounts.kind.apiKeyOf', {
          provider: providerName(props.account.provider) ?? '?',
        })
      : t(`accounts.kind.line.${props.account.kind}`),
  )

  /** An API key's routing rule, for its tooltip: "Used when a task asks for Anthropic (provider: anthropic)." */
  const routing = computed(() =>
    props.account.kind === 'api-key' && props.account.provider
      ? t('accounts.routing', {
          service: providerName(props.account.provider) ?? props.account.provider,
          id: props.account.provider,
        })
      : '',
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
    const priority: MenuItem[] = toValue(priorityInMenu)
      ? [
          {
            id: 'priority',
            label: t('accounts.action.priority', { n: a.priority }),
            icon: 'settings',
          },
        ]
      : []
    const remove: MenuItem = {
      id: 'delete',
      label: t('accounts.action.delete'),
      icon: 'trash',
      danger: true,
      separator: true,
    }
    if (!canSignIn(a)) {
      return [
        ...(a.keyHint
          ? [{ id: 'test', label: t('accounts.action.test'), icon: 'refresh' } as const]
          : []),
        {
          id: 'key',
          label: t(a.keyHint ? 'accounts.action.replaceKey' : 'accounts.action.addKey'),
          icon: 'settings',
        },
        ...(a.provider
          ? [{ id: 'model', label: t('accounts.action.model'), icon: 'gauge' } as const]
          : []),
        ...(a.spendSource !== 'free'
          ? [{ id: 'budget', label: t('accounts.action.budget'), icon: 'quota' } as const]
          : []),
        { id: 'rename', label: t('accounts.action.rename'), icon: 'accounts' },
        ...priority,
        {
          id: a.enabled ? 'turnOff' : 'turnOn',
          label: t(a.enabled ? 'accounts.action.turnOff' : 'accounts.action.turnOn'),
          icon: 'workers',
        },
        remove,
      ]
    }
    const items: MenuItem[] = [
      { id: 'refresh', label: t('accounts.action.refresh'), icon: 'refresh' },
      { id: 'rename', label: t('accounts.action.rename'), icon: 'accounts' },
      ...priority,
      { id: 'login', label: t('accounts.action.login'), icon: 'external' },
    ]
    if (a.kind === 'isolated' && a.status !== 'needs_login') {
      items.push({ id: 'logout', label: t('accounts.action.logout'), icon: 'arrowLeft' })
    }
    items.push(remove)
    return items
  })

  const DIALOGS: ReadonlySet<string> = new Set([
    'rename',
    'key',
    'delete',
    'logout',
    'priority',
    'budget',
    'model',
  ])

  function onMenu(item: string) {
    const a = props.account
    if (item === 'refresh') void actions.run('refresh', () => accounts.refresh(a.id))
    else if (item === 'login') void actions.run('login', () => accounts.login(a.id))
    else if (item === 'test') void keyTest.test(a)
    else if (item === 'turnOff') setEnabled(false)
    else if (item === 'turnOn') setEnabled(true)
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

  return {
    actions,
    session,
    kindLabel,
    routing,
    next,
    menu,
    onMenu,
    setEnabled,
    setPriority,
    testing: computed(() => keyTest.isTesting(props.account.id)),
  }
}
