<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import DaemonSaid from '@/components/DaemonSaid.vue'

import { causeKey } from './causeText'

/**
 * A failed GitHub or git step: what went wrong and what to do, in the reader's language from the cause code, with
 * the daemon's own (English, git's or GitHub's) message behind "Seelegent said:".
 */
defineProps<{ cause?: string; text: string }>()

const { t } = useI18n()
</script>

<template>
  <div class="cause" role="alert">
    <p class="cause__why">{{ t(causeKey(cause)) }}</p>
    <DaemonSaid v-if="text" :text="text" />
  </div>
</template>

<style scoped>
.cause {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.cause__why {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
  overflow-wrap: anywhere;
}
</style>
