<script setup lang="ts">
import { computed } from 'vue'

import type { Tone } from './tone'

/**
 * A horizontal bar for a fraction 0..1 (role=progressbar). Without `value` it is indeterminate.
 * `label` names it for screen readers; `valueText` overrides the announced value ("42% used").
 */
const props = withDefaults(
  defineProps<{
    value?: number
    tone?: Tone
    label: string
    valueText?: string
    size?: 'sm' | 'md'
  }>(),
  { value: undefined, tone: 'running', valueText: undefined, size: 'md' },
)

const clamped = computed(() =>
  props.value === undefined ? undefined : Math.min(1, Math.max(0, props.value)),
)
</script>

<template>
  <div
    class="progress"
    :class="[
      `tone-${tone}`,
      `progress--${size}`,
      { 'progress--indeterminate': clamped === undefined },
    ]"
    role="progressbar"
    :aria-label="label"
    aria-valuemin="0"
    aria-valuemax="100"
    :aria-valuenow="clamped === undefined ? undefined : Math.round(clamped * 100)"
    :aria-valuetext="valueText"
  >
    <span
      class="progress__fill"
      :style="clamped === undefined ? undefined : { width: `${clamped * 100}%` }"
    />
  </div>
</template>

<style scoped>
.progress {
  position: relative;
  width: 100%;
  height: 6px;
  overflow: hidden;
  border-radius: var(--radius-pill);
  background: var(--bg-surface-2);
  box-shadow: inset 0 0 0 var(--border-width) var(--border-subtle);
}

.progress--sm {
  height: 4px;
}

.progress__fill {
  position: absolute;
  inset: 0 auto 0 0;
  border-radius: inherit;
  background: var(--tone-fg);
  transition: width var(--duration-base) var(--ease-standard);
}

.progress--indeterminate .progress__fill {
  width: 35%;
  animation: progress-slide 1.2s var(--ease-standard) infinite;
}

@keyframes progress-slide {
  from {
    transform: translateX(-100%);
  }
  to {
    transform: translateX(300%);
  }
}
</style>
