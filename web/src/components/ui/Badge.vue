<script setup lang="ts">
import { computed } from 'vue'

import type { Tone } from './tone'

const props = withDefaults(
  defineProps<{
    /** A number shows a count (99+ above 99); without it the badge is a dot. */
    count?: number
    tone?: Tone
    /** Screen-reader text, e.g. "3 tasks waiting for review"; the visible number is hidden from them. */
    label?: string
  }>(),
  { count: undefined, tone: 'attention', label: undefined },
)

const text = computed(() =>
  props.count === undefined ? '' : props.count > 99 ? '99+' : String(props.count),
)
</script>

<template>
  <span class="badge" :class="[`tone-${tone}`, { 'badge--dot': count === undefined }]">
    <span aria-hidden="true">{{ text }}</span>
    <span v-if="label" class="sr-only">{{ label }}</span>
  </span>
</template>

<style scoped>
.badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 18px;
  height: 18px;
  padding: 0 var(--space-1);
  border-radius: var(--radius-pill);
  background: var(--tone-fg);
  color: var(--bg-surface);
  font-size: var(--fs-2xs);
  font-weight: var(--fw-bold);
  font-variant-numeric: tabular-nums;
  line-height: 1;
}

.badge--dot {
  min-width: 8px;
  width: 8px;
  height: 8px;
  padding: 0;
}
</style>
