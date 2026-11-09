<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type { Provider } from '@/api/types'
import ProviderMark from '@/components/ui/ProviderMark.vue'

/**
 * The service picker closed to one line: the chosen service, the CLI it runs through, and "Change", which opens
 * the list again (ServicePicker).
 */
defineProps<{ name: string; backend: Provider['backend'] }>()
const emit = defineEmits<{ change: [] }>()

const { t } = useI18n()
</script>

<template>
  <div class="chosen">
    <span class="chosen__label">{{ t('accounts.service.label') }}</span>
    <p class="chosen__line">
      <strong>{{ name }}</strong>
      <span class="chosen__cli">
        <ProviderMark :backend="backend" :label="false" />
        {{ t('accounts.service.runsOn', { cli: t(`accounts.backend.${backend}`) }) }}
      </span>
      <button
        type="button"
        class="chosen__change"
        :aria-label="t('accounts.service.changeOf', { name })"
        @click="emit('change')"
      >
        {{ t('accounts.service.change') }}
      </button>
    </p>
  </div>
</template>

<style scoped>
.chosen {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.chosen__label {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.chosen__line {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) var(--space-3);
  min-height: var(--control-md);
  padding: var(--space-1) var(--space-3);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
  font-size: var(--fs-sm);
}

.chosen__cli {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.chosen__change {
  margin-left: auto;
  padding: 0;
  border: 0;
  background: none;
  color: var(--link);
  font: inherit;
  font-size: var(--fs-xs);
  text-decoration: underline;
  cursor: pointer;
}
</style>
