<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import Button from '@/components/ui/Button.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Spinner from '@/components/ui/Spinner.vue'
import type { IconName } from '@/components/ui/icons'

import type { ProcessModel } from './useProcess'

/**
 * The states the Tree, Graph and Files views share: loading, a failed read with retry, nothing yet, and the note
 * that only the newest steps are loaded (with a button to load earlier ones). The view itself goes in the slot.
 */
defineProps<{
  model: ProcessModel
  empty: boolean
  icon: IconName
  emptyTitle: string
  emptyDesc: string
}>()

const { t } = useI18n()
</script>

<template>
  <div class="pf">
    <p v-if="model.state.status === 'loading' && empty" class="pf__state">
      <Spinner size="sm" decorative />
      {{ t('process.loading') }}
    </p>
    <div v-else-if="model.state.status === 'error'" class="pf__state" role="alert">
      <span>{{ t('terminal.loadError', { error: model.state.error ?? '' }) }}</span>
      <Button size="sm" @click="model.retry">{{ t('common.action.retry') }}</Button>
    </div>
    <EmptyState
      v-else-if="empty && !model.partial.value"
      :icon="icon"
      :level="3"
      :title="emptyTitle"
      :description="emptyDesc"
    />
    <template v-else>
      <div v-if="model.partial.value" class="pf__partial">
        <span>{{ t('process.partial') }}</span>
        <Button
          size="sm"
          variant="ghost"
          :loading="model.state.loadingEarlier"
          @click="model.loadEarlier"
        >
          {{ t('process.loadEarlier') }}
        </Button>
      </div>
      <slot />
    </template>
  </div>
</template>

<style scoped>
.pf {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 0;
  overflow: auto;
}

.pf__state {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-6) var(--space-4);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.pf__partial {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-4);
  border-bottom: var(--border-width) solid var(--border-subtle);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}
</style>
