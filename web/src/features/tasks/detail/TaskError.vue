<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Attention } from '@/api/types'
import DaemonSaid from '@/components/DaemonSaid.vue'

/**
 * A failed task's error. When the attention banner already says in the reader's language how the worker ended (a
 * failed flag with a coded kind: no result, exit code, timeout...), the daemon's English line is only kept behind
 * the "Seelegent said" disclosure rather than repeated as the main text.
 */
const props = defineProps<{ error: string; attention?: Attention | null }>()

const { t } = useI18n()
const worded = computed(
  () => props.attention?.reason === 'failed' && !!props.attention.params?.kind,
)
</script>

<template>
  <div v-if="worded" class="te__said">
    <DaemonSaid :text="error" />
  </div>
  <section v-else class="te" role="alert">
    <strong>{{ t('task.detail.error') }}</strong>
    <pre class="te__text">{{ error }}</pre>
  </section>
</template>

<style scoped>
.te {
  margin: 0 var(--space-5) var(--space-3);
  padding: var(--space-3) var(--space-4);
  border: var(--border-width) solid var(--status-danger-border);
  border-radius: var(--radius-lg);
  background: var(--status-danger-bg);
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
}

.te__text {
  margin-top: var(--space-1);
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.te__said {
  margin: 0 var(--space-5) var(--space-3);
}

@media (width < 600px) {
  .te,
  .te__said {
    margin: 0 var(--space-4) var(--space-3);
  }
}
</style>
