<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import QuotaBar from '@/components/ui/QuotaBar.vue'
import { useFormat } from '@/composables/useFormat'
import { ACCOUNT_TONE, accountState, tightestWindow, windowName } from '@/lib/accountState'
import { useAccountsStore } from '@/stores/accounts'

/**
 * One tile per account: its state, how busy it is, and its tightest quota window (the one that runs out first),
 * so a glance tells whether new work can start. The tiles link to Accounts, where they are managed.
 */
const props = defineProps<{ now: number }>()

const { t } = useI18n()
const fmt = useFormat()
const accounts = useAccountsStore()

const tiles = computed(() =>
  accounts.accounts.map((account) => {
    const state = accountState(account, props.now)
    const twin = account.twinOf ? (accounts.get(account.twinOf)?.label ?? account.twinOf) : ''
    return { account, state, twin, window: tightestWindow(account) }
  }),
)
</script>

<template>
  <section class="strip" aria-labelledby="watch-accounts">
    <div class="strip__head">
      <h2 id="watch-accounts" class="strip__title">{{ t('watch.accounts.title') }}</h2>
      <RouterLink to="/accounts" class="strip__manage">{{ t('watch.accounts.manage') }}</RouterLink>
    </div>
    <p v-if="!tiles.length" class="strip__empty">{{ t('watch.accounts.empty') }}</p>
    <ul v-else class="strip__list">
      <li v-for="{ account, state, twin, window } in tiles" :key="account.id" class="tile">
        <div class="tile__head">
          <span class="tile__label">{{ account.label }}</span>
          <span class="tile__state" :class="`tone-${ACCOUNT_TONE[state]}`">
            {{ t(`watch.accounts.state.${state}`) }}
          </span>
        </div>
        <QuotaBar
          v-if="window"
          :used="1 - window.remaining"
          mode="left"
          :warn-at="0.85"
          :label="windowName(window)"
        />
        <p v-else class="tile__none">{{ t('watch.accounts.noQuota') }}</p>
        <p v-if="state === 'twin'" class="tile__note">
          {{ t('watch.accounts.twin', { name: twin }) }}
        </p>
        <p v-else-if="account.quota.error" class="tile__note" :title="account.quota.error.message">
          {{ t('watch.accounts.readFailed') }}
        </p>
        <p class="tile__foot">
          <span>{{
            t('watch.accounts.busy', { busy: account.busy, max: account.maxConcurrent })
          }}</span>
          <span v-if="window?.resetAt">
            {{ t('watch.accounts.resets', { time: fmt.relative(window.resetAt, now) }) }}
          </span>
        </p>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.strip {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.strip__head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-3);
}

.strip__title {
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
}

.strip__manage {
  color: var(--accent);
  font-size: var(--fs-sm);
}

.strip__empty {
  color: var(--text-muted);
  font-size: var(--fs-sm);
}

.strip__list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: var(--space-2);
  list-style: none;
}

.tile {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
  padding: var(--space-2) var(--space-3);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
}

.tile__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  min-width: 0;
}

.tile__label {
  overflow: hidden;
  font-size: var(--fs-sm);
  font-weight: var(--fw-medium);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tile__state {
  flex: none;
  padding: 0 var(--space-2);
  border-radius: var(--radius-pill);
  background: var(--tone-bg);
  color: var(--tone-fg);
  font-size: var(--fs-2xs);
  line-height: 18px;
}

.tile__note {
  color: var(--text-secondary);
  font-size: var(--fs-2xs);
}

.tile__none,
.tile__foot {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: var(--space-1) var(--space-2);
  color: var(--text-muted);
  font-size: var(--fs-2xs);
}
</style>
