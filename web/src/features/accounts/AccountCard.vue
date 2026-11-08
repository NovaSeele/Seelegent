<script setup lang="ts">
import { computed, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Account } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import IconButton from '@/components/ui/IconButton.vue'
import Menu, { type MenuItem } from '@/components/ui/Menu.vue'
import NumberField from '@/components/ui/NumberField.vue'
import StatusPill from '@/components/ui/StatusPill.vue'
import Switch from '@/components/ui/Switch.vue'
import { useAccountsStore } from '@/stores/accounts'

import AccountFacts from './AccountFacts.vue'
import { canSignIn, offConsequence, useActions } from './accountView'
import LoginPanel from './LoginPanel.vue'
import QuotaGroups from './QuotaGroups.vue'
import { useAccountState } from './useAccountState'

export type AccountDialog = 'rename' | 'key' | 'delete' | 'logout' | 'off'

/**
 * One account: its status (contract wording), what it is doing (busy, resting, out of quota), its quota windows,
 * a running sign-in, and its controls: on/off, priority, the next step its status asks for (sign in, set a key)
 * and a menu with the rest. Dialogs (rename, API key, delete, sign out, and turning off when that changes something
 * now) belong to the page; the card asks for them.
 */
const props = defineProps<{ account: Account; now: number }>()
const emit = defineEmits<{ dialog: [kind: AccountDialog] }>()

const { t } = useI18n()
const accounts = useAccountsStore()
const actions = useActions()
const id = useId()

const session = computed(() => accounts.logins[props.account.id])
const { pill } = useAccountState(props)
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

function onMenu(item: string) {
  const a = props.account
  if (item === 'refresh') void actions.run('refresh', () => accounts.refresh(a.id))
  else if (item === 'login') void actions.run('login', () => accounts.login(a.id))
  else if (item === 'rename' || item === 'key' || item === 'delete' || item === 'logout') {
    emit('dialog', item)
  }
}

// Turning off asks first only when it changes something now: turns run on it, or nothing else takes its work.
function setEnabled(enabled: boolean) {
  if (!enabled && offConsequence(props.account, accounts.accounts)) {
    emit('dialog', 'off')
    return
  }
  void actions.run('enabled', () => accounts.update(props.account.id, { enabled }))
}

function setPriority(priority: number) {
  void actions.run('priority', () => accounts.update(props.account.id, { priority }))
}
</script>

<template>
  <article class="card" :class="{ 'card--off': !account.enabled }" :aria-labelledby="`${id}-name`">
    <header class="card__head">
      <div class="card__title">
        <h3 :id="`${id}-name`" class="card__name">{{ account.label }}</h3>
        <p class="card__meta">
          <span>{{ kindLabel }}</span>
          <span v-if="account.keyHint">{{ t('accounts.keyHint', { hint: account.keyHint }) }}</span>
          <span v-if="account.email" class="card__email">{{ account.email }}</span>
          <span v-if="account.quota.plan">{{ account.quota.plan }}</span>
        </p>
      </div>
      <StatusPill :tone="pill.tone" :label="pill.label" size="sm" />
    </header>

    <AccountFacts :account="account" :now="now" :has-session="!!session" />

    <QuotaGroups :account="account" :now="now" />
    <LoginPanel v-if="session" :account="account" :session="session" />

    <div v-if="next" class="card__next">
      <Button
        v-if="next === 'login'"
        size="sm"
        variant="primary"
        icon="external"
        :loading="actions.isBusy('login')"
        @click="onMenu('login')"
      >
        {{ t('accounts.action.login') }}
      </Button>
      <Button v-else size="sm" variant="primary" @click="emit('dialog', 'key')">
        {{ t('accounts.action.key') }}
      </Button>
    </div>

    <footer class="card__foot">
      <Switch
        :model-value="account.enabled"
        :label="t('accounts.enabled')"
        :disabled="actions.isBusy('enabled')"
        @update:model-value="setEnabled"
      />
      <NumberField
        :model-value="account.priority"
        :label="t('accounts.priority')"
        size="sm"
        integer
        inline
        :disabled="actions.isBusy('priority')"
        @commit="setPriority"
      />
      <span class="card__spacer" />
      <Menu
        :items="menu"
        :label="t('accounts.action.more', { name: account.label })"
        @select="onMenu"
      >
        <template #trigger="{ attrs }">
          <IconButton
            v-bind="attrs"
            icon="more"
            size="sm"
            variant="secondary"
            :label="t('accounts.action.more', { name: account.label })"
            :tooltip="false"
          />
        </template>
      </Menu>
    </footer>
  </article>
</template>

<style scoped>
.card {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  min-width: 0;
  padding: var(--space-4);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
}

.card--off .card__name,
.card--off :deep(.quota) {
  opacity: 0.7;
}

.card__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-2);
}

.card__title {
  min-width: 0;
}

.card__name {
  overflow: hidden;
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.card__meta {
  display: flex;
  flex-wrap: wrap;
  gap: 0 var(--space-2);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.card__meta > span + span::before {
  margin-right: var(--space-2);
  content: '·';
  color: var(--text-muted);
}

.card__email {
  overflow-wrap: anywhere;
}

.card__next {
  display: flex;
}

.card__foot {
  margin-top: auto;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-3);
  --num-inline-width: 64px;
  padding-top: var(--space-3);
  border-top: var(--border-width) solid var(--border-subtle);
}

.card__spacer {
  flex: 1;
}
</style>
