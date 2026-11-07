<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type { Account } from '@/api/types'
import QuotaBar from '@/components/ui/QuotaBar.vue'
import { useFormat } from '@/composables/useFormat'

/**
 * Every quota window of an account, grouped (a group "<group> <family>" is a sub-limit of <group>), each with
 * what is left and when it resets. API-key accounts have no readable quota: their limits are only seen from runs
 * (passive), which is said instead of empty bars.
 */
defineProps<{ account: Account; now: number }>()

const { t } = useI18n()
const fmt = useFormat()
</script>

<template>
  <div class="quota">
    <p v-if="account.quota.noPlan" class="quota__note quota__note--box tone-danger">
      {{ account.quota.noPlanReason || t('accounts.quota.noPlan') }}
    </p>
    <p v-if="account.quota.source === 'passive'" class="quota__note">
      {{ t('accounts.quota.passive') }}
    </p>
    <p v-else-if="!account.quota.groups.length && !account.quota.noPlan" class="quota__note">
      {{ account.status === 'new' ? t('accounts.quota.checking') : t('accounts.quota.none') }}
    </p>
    <div v-for="group in account.quota.groups" :key="group.name" class="quota__group">
      <h4 class="quota__name">{{ group.name }}</h4>
      <div v-for="window in group.windows" :key="window.window" class="quota__window">
        <QuotaBar
          :used="1 - window.remaining"
          mode="left"
          :warn-at="0.85"
          :label="window.label || t('accounts.quota.window', { window: window.window })"
        />
        <span v-if="window.resetAt" class="quota__reset">
          {{ t('accounts.quota.resets', { time: fmt.relative(window.resetAt, now) }) }}
        </span>
      </div>
    </div>
    <p v-if="account.quota.error" class="quota__note quota__note--box tone-attention" role="status">
      {{ t('accounts.quota.readFailed', { error: account.quota.error.message }) }}
    </p>
    <p v-if="account.quota.fetchedAt" class="quota__fetched">
      {{
        t(account.quota.source === 'passive' ? 'accounts.quota.seen' : 'accounts.quota.fetched', {
          time: fmt.relative(account.quota.fetchedAt, now),
        })
      }}
    </p>
  </div>
</template>

<style scoped>
.quota {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}

.quota__group {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.quota__name {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.quota__window {
  display: flex;
  flex-direction: column;
  gap: var(--space-0);
}

.quota__reset,
.quota__fetched {
  color: var(--text-muted);
  font-size: var(--fs-2xs);
}

.quota__note {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.quota__note--box {
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm);
  background: var(--tone-bg);
  color: var(--tone-fg);
}
</style>
