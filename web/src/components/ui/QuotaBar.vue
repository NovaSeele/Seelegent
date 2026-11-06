<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { useFormat } from '@/composables/useFormat'

import ProgressBar from './ProgressBar.vue'
import type { Tone } from './tone'

/**
 * Quota use for one bucket. Neutral while there is room, attention from `warnAt` (default 80% used, the old
 * dashboard's warning threshold), danger when exhausted. Shows "used" or "left" per `mode`.
 */
const props = withDefaults(
  defineProps<{
    /** Fraction used, 0..1. */
    used: number
    label: string
    mode?: 'used' | 'left'
    warnAt?: number
    showValue?: boolean
  }>(),
  { mode: 'used', warnAt: 0.8, showValue: true },
)

const { t } = useI18n()
const format = useFormat()

const usedClamped = computed(() => Math.min(1, Math.max(0, props.used)))
const tone = computed<Tone>(() =>
  usedClamped.value >= 1 ? 'danger' : usedClamped.value >= props.warnAt ? 'attention' : 'neutral',
)
const shown = computed(() => (props.mode === 'used' ? usedClamped.value : 1 - usedClamped.value))
const text = computed(() =>
  t(props.mode === 'used' ? 'common.ui.quotaUsed' : 'common.ui.quotaLeft', {
    value: format.value.percent(shown.value),
  }),
)
</script>

<template>
  <div class="quota">
    <div class="quota__head">
      <span class="quota__label">{{ label }}</span>
      <span v-if="showValue" class="quota__value" :class="`tone-${tone}`">{{ text }}</span>
    </div>
    <ProgressBar :value="shown" :tone="tone" :label="label" :value-text="text" size="sm" />
  </div>
</template>

<style scoped>
.quota {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.quota__head {
  display: flex;
  justify-content: space-between;
  gap: var(--space-2);
  font-size: var(--fs-xs);
}

.quota__label {
  overflow: hidden;
  color: var(--text-secondary);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.quota__value {
  flex: none;
  color: var(--text-secondary);
  font-variant-numeric: tabular-nums;
}

.quota__value.tone-attention,
.quota__value.tone-danger {
  color: var(--tone-fg);
  font-weight: var(--fw-medium);
}
</style>
