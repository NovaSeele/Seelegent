<script setup lang="ts">
import { computed } from 'vue'

import { formatKeySpec } from '@/lib/keys'

const props = withDefaults(
  defineProps<{
    /** A key spec such as "Mod+K" or "g s". */
    keys: string
    size?: 'sm' | 'md'
  }>(),
  { size: 'md' },
)

const steps = computed(() => formatKeySpec(props.keys))
</script>

<template>
  <span class="kbd" :class="`kbd--${size}`">
    <template v-for="(step, i) in steps" :key="i">
      <span v-if="i > 0" class="kbd__then" aria-hidden="true">›</span>
      <kbd v-for="key in step" :key="key" class="kbd__key">{{ key }}</kbd>
    </template>
  </span>
</template>

<style scoped>
.kbd {
  display: inline-flex;
  align-items: center;
  gap: var(--space-0);
  white-space: nowrap;
}

.kbd__key {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 20px;
  height: 20px;
  padding: 0 var(--space-1);
  border: var(--border-width) solid var(--border-strong);
  border-bottom-width: 2px;
  border-radius: var(--radius-xs);
  background: var(--bg-surface-2);
  color: var(--text-secondary);
  font-family: var(--font-sans);
  font-size: var(--fs-2xs);
  font-weight: var(--fw-medium);
  line-height: 1;
}

.kbd--sm .kbd__key {
  min-width: 18px;
  height: 18px;
}

.kbd__then {
  color: var(--text-muted);
  font-size: var(--fs-2xs);
  padding: 0 var(--space-0);
}
</style>
