<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import Button from '@/components/ui/Button.vue'
import { useFormat } from '@/composables/useFormat'

import type { Step } from './process'
import StageTag from './StageTag.vue'

/** The step picked in the Graph view (announced politely), or a hint to pick one. */
defineProps<{ step: Step | null; label: string; durationMs: number }>()
const emit = defineEmits<{ reveal: [key: string] }>()

const { t } = useI18n()
const format = useFormat()
</script>

<template>
  <section class="detail" aria-live="polite">
    <template v-if="step">
      <h3 class="sr-only">{{ t('process.graph.selected') }}</h3>
      <StageTag :stage="step.stage" />
      <span class="detail__name">{{ step.name }}</span>
      <code class="detail__label">{{ label }}</code>
      <span class="detail__meta">
        {{ t(`process.state.${step.state}`) }} · {{ format.time(step.start) }}
        <template v-if="durationMs"> · {{ format.duration(durationMs) }}</template>
      </span>
      <Button size="sm" variant="ghost" @click="emit('reveal', step.key)">
        {{ t('process.reveal') }}
      </Button>
    </template>
    <span v-else class="detail__hint">{{ t('process.graph.pick') }}</span>
  </section>
</template>

<style scoped>
.detail {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  min-height: var(--control-md);
  font-size: var(--fs-xs);
}

.detail__name {
  color: var(--text-secondary);
}

.detail__label {
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  font-family: var(--font-mono);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.detail__meta,
.detail__hint {
  color: var(--text-muted);
}
</style>
