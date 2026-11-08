<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type { Account } from '@/api/types'

import { isExhausted, showsSlots } from './accountView'
import { useAccountState } from './useAccountState'

/**
 * What an account is doing, as chips (busy slots, out of quota or resting when the pill does not already say
 * so, signing in elsewhere), and the notes its status needs (an old quota read without a reason, a twin).
 */
const props = defineProps<{ account: Account; now: number; hasSession: boolean }>()

const { t } = useI18n()
const { twinLabel, exhaustedLabel, restingLabel, holdHint, pill } = useAccountState(props)
</script>

<template>
  <ul class="facts">
    <li
      v-if="showsSlots(account)"
      :class="account.busy > 0 ? 'tone-running' : 'tone-neutral'"
      class="facts__chip"
    >
      {{ t('accounts.busy', { busy: account.busy, max: account.maxConcurrent }) }}
    </li>
    <template v-if="pill.state === 'status'">
      <li v-if="isExhausted(account, now)" class="facts__chip tone-attention">
        {{ exhaustedLabel }}
      </li>
      <li v-else-if="account.resting" class="facts__chip tone-neutral">{{ restingLabel }}</li>
    </template>
    <li v-if="account.signingIn && !hasSession" class="facts__chip tone-neutral">
      {{ t('accounts.signingIn') }}
    </li>
  </ul>

  <!-- A hold says what to do: no time ends it. -->
  <p v-if="holdHint" class="facts__note">{{ holdHint }}</p>
  <!-- A failed read with a reason is said once, in the quota box below. -->
  <p v-if="account.status === 'error' && !account.quota.error" class="facts__note">
    {{ t('accounts.errorNote') }}
  </p>
  <p v-if="account.status === 'twin'" class="facts__note">
    {{ t('accounts.twinNote', { name: twinLabel }) }}
  </p>
</template>

<style scoped>
.facts {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  margin: 0;
  padding: 0;
  list-style: none;
}

/* No chips (an account that cannot run): no empty row and gap. */
.facts:empty {
  display: none;
}

.facts__chip {
  padding: 0 var(--space-2);
  border-radius: var(--radius-pill);
  background: var(--tone-bg);
  color: var(--tone-fg);
  font-size: var(--fs-2xs);
  line-height: 20px;
}

.facts__note {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}
</style>
