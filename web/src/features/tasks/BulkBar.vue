<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import Button from '@/components/ui/Button.vue'

import type { BulkAction } from './BulkDialog.vue'

/** The toolbar shown while selecting: how many rows are chosen and the bulk actions that apply to them. */
defineProps<{ selected: number; cancel: number; accept: number }>()
defineEmits<{ run: [action: BulkAction] }>()

const { t } = useI18n()
</script>

<template>
  <div class="bulk" role="toolbar" :aria-label="t('tasks.select.toolbar')">
    <span class="bulk__count" aria-live="polite">{{ t('tasks.select.count', selected) }}</span>
    <Button size="sm" variant="danger" :disabled="!cancel" @click="$emit('run', 'cancel')">
      {{ t('tasks.bulk.cancel', { count: cancel }) }}
    </Button>
    <Button size="sm" :disabled="!accept" @click="$emit('run', 'accept')">
      {{ t('tasks.bulk.accept', { count: accept }) }}
    </Button>
  </div>
</template>

<style scoped>
.bulk {
  position: sticky;
  top: 0;
  z-index: 1;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border-block: var(--border-width) solid var(--border-subtle);
  background: var(--bg-surface-2);
}

.bulk__count {
  flex: 1;
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}
</style>
