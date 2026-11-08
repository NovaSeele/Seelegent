<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Account } from '@/api/types'
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import Button from '@/components/ui/Button.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import ProviderMark from '@/components/ui/ProviderMark.vue'
import { useNow } from '@/composables/useNow'
import { useAccountsStore } from '@/stores/accounts'

import AccountCard, { type AccountDialog } from './AccountCard.vue'
import AccountDialogs from './AccountDialogs.vue'
import { BACKENDS, isExhausted, readyForWork, useActions } from './accountView'
import AddAccountDialog from './AddAccountDialog.vue'
import ProvidersPanel from './ProvidersPanel.vue'

/**
 * Accounts: a summary of what can take work now, then the accounts grouped by backend (status, quota, sign-in,
 * controls), then the API providers the API-key accounts use. Everything stays live over SSE (account.updated,
 * account.login, providers.changed).
 */
const { t } = useI18n()
const accounts = useAccountsStore()
const actions = useActions()
const now = useNow(30_000)

const adding = ref(false)
const dialog = ref<AccountDialog | null>(null)
const dialogId = ref('')
const dialogAccount = computed(() => accounts.get(dialogId.value))

const groups = computed(() =>
  BACKENDS.map((backend) => ({
    backend,
    items: accounts.accounts.filter((a) => a.backend === backend),
  })).filter((group) => group.items.length > 0),
)

const needsYou = (a: Account) =>
  a.enabled && ['needs_login', 'no_key', 'no_plan'].includes(a.status)

const summary = computed(() => {
  const list = accounts.accounts
  return [
    { id: 'ready', value: list.filter((a) => readyForWork(a, now.value)).length },
    { id: 'busy', value: list.reduce((sum, a) => sum + a.busy, 0) },
    {
      id: 'resting',
      value: list.filter((a) => a.enabled && (a.resting || isExhausted(a, now.value))).length,
    },
    { id: 'needsYou', value: list.filter(needsYou).length },
  ]
})

function openDialog(account: Account, kind: AccountDialog) {
  dialogId.value = account.id
  dialog.value = kind
}

function refreshAll() {
  void actions.run('all', () => accounts.refreshAll(), t('accounts.done.refreshedAll'))
}

// A sign-in started before this page was opened (or before a reload): show its panel again.
function resumeLogins() {
  for (const account of accounts.accounts) {
    if (account.signingIn && !accounts.logins[account.id]) void accounts.resumeLogin(account.id)
  }
}

onMounted(resumeLogins)
watch(() => accounts.accounts.filter((a) => a.signingIn).length, resumeLogins)
</script>

<template>
  <PageContainer>
    <PageHeader :title="t('nav.accounts.label')" :description="t('nav.accounts.desc')">
      <template #actions>
        <Button
          variant="secondary"
          icon="refresh"
          :loading="actions.isBusy('all')"
          :disabled="!accounts.accounts.length"
          @click="refreshAll"
        >
          {{ t('accounts.action.refreshAll') }}
        </Button>
        <Button variant="primary" icon="plus" @click="adding = true">
          {{ t('accounts.action.add') }}
        </Button>
      </template>
    </PageHeader>

    <div class="accounts">
      <EmptyState
        v-if="!accounts.accounts.length"
        icon="accounts"
        :title="t('accounts.empty.title')"
        :description="t('accounts.empty.desc')"
      />
      <template v-else>
        <dl class="accounts__summary">
          <div v-for="item in summary" :key="item.id" class="tile">
            <dt class="tile__label">{{ t(`accounts.summary.${item.id}`) }}</dt>
            <dd class="tile__value">{{ item.value }}</dd>
          </div>
        </dl>

        <section
          v-for="group in groups"
          :key="group.backend"
          class="accounts__group"
          :aria-labelledby="`accounts-${group.backend}`"
        >
          <h2 :id="`accounts-${group.backend}`" class="accounts__heading">
            <ProviderMark :backend="group.backend" :label="false" />
            {{ t(`accounts.backend.${group.backend}`) }}
            <span class="accounts__count">{{ group.items.length }}</span>
          </h2>
          <div class="accounts__grid">
            <AccountCard
              v-for="account in group.items"
              :key="account.id"
              :account="account"
              :now="now"
              @dialog="openDialog(account, $event)"
            />
          </div>
        </section>
      </template>

      <ProvidersPanel />
    </div>

    <AddAccountDialog v-model:open="adding" />
    <AccountDialogs v-model="dialog" :account="dialogAccount" />
  </PageContainer>
</template>

<style scoped>
.accounts {
  display: flex;
  flex-direction: column;
  gap: var(--space-6);
  padding: 0 var(--space-6) var(--space-7);
}

.accounts__summary {
  display: grid;
  margin: 0;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: var(--space-2);
}

.tile {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding: var(--space-3) var(--space-4);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
}

.tile__label {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.tile__value {
  margin: 0;
  font-size: var(--fs-xl);
  font-weight: var(--fw-semibold);
  font-variant-numeric: tabular-nums;
}

.accounts__group {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.accounts__heading {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
}

.accounts__count {
  min-width: 20px;
  padding: 0 var(--space-1);
  border-radius: var(--radius-pill);
  background: var(--neutral-bg);
  color: var(--neutral-fg);
  font-size: var(--fs-2xs);
  line-height: 20px;
  text-align: center;
}

.accounts__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 340px), 1fr));
  gap: var(--space-3);
}

@media (width < 600px) {
  .accounts {
    padding: 0 var(--space-4) var(--space-6);
  }

  .accounts__summary {
    grid-template-columns: repeat(2, 1fr);
  }
}
</style>
