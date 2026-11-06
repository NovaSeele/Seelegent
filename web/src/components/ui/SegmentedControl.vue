<script setup lang="ts" generic="T extends string | number">
import { computed, ref } from 'vue'

import { useRovingFocus } from '@/composables/useRovingFocus'

import Icon from './Icon.vue'
import type { IconName } from './icons'

export interface SegmentOption<V> {
  value: V
  label: string
  icon?: IconName
  /** Show only the icon; the label stays as the accessible name. */
  iconOnly?: boolean
  disabled?: boolean
}

/** A single-choice control drawn as joined segments (role=radiogroup, arrow keys move and select). */
const model = defineModel<T>({ required: true })

const props = withDefaults(
  defineProps<{
    options: readonly SegmentOption<T>[]
    label: string
    size?: 'sm' | 'md'
  }>(),
  { size: 'md' },
)

const group = ref<HTMLElement | null>(null)
const options = computed(() => props.options)
const { onKeydown } = useRovingFocus(options, (v) => (model.value = v), group)
</script>

<template>
  <div
    ref="group"
    class="segmented"
    :class="`segmented--${size}`"
    role="radiogroup"
    :aria-label="label"
  >
    <button
      v-for="(option, index) in options"
      :key="String(option.value)"
      type="button"
      role="radio"
      class="segmented__option"
      data-roving
      :aria-checked="option.value === model"
      :aria-label="option.iconOnly ? option.label : undefined"
      :title="option.iconOnly ? option.label : undefined"
      :tabindex="option.value === model ? 0 : -1"
      :disabled="option.disabled"
      @click="model = option.value"
      @keydown="onKeydown($event, index)"
    >
      <Icon v-if="option.icon" :name="option.icon" class="segmented__icon" />
      <span v-if="!option.iconOnly">{{ option.label }}</span>
    </button>
  </div>
</template>

<style scoped>
.segmented {
  --pad: var(--space-0);
  display: inline-flex;
  max-width: 100%;
  padding: var(--pad);
  gap: var(--pad);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-surface-2);
}

.segmented__option {
  display: inline-flex;
  flex: 1 1 auto;
  align-items: center;
  justify-content: center;
  gap: var(--space-1);
  min-width: 0;
  min-height: max(
    calc(var(--control-md) - 2 * var(--pad) - 2 * var(--border-width)),
    var(--target-min)
  );
  padding: 0 var(--space-3);
  border: 0;
  /* Concentric with the group: outer radius minus the inset. */
  border-radius: max(0px, calc(var(--radius-md) - var(--pad)));
  background: transparent;
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  font-weight: var(--fw-medium);
  white-space: nowrap;
  cursor: pointer;
  transition:
    background-color var(--transition-fast),
    color var(--transition-fast);
}

.segmented--sm .segmented__option {
  min-height: max(
    calc(var(--control-sm) - 2 * var(--pad) - 2 * var(--border-width)),
    var(--target-min)
  );
  padding: 0 var(--space-2);
  font-size: var(--fs-xs);
}

.segmented__option:hover:not(:disabled) {
  color: var(--text-primary);
}

.segmented__option[aria-checked='true'] {
  background: var(--bg-surface);
  box-shadow: var(--shadow-sm);
  color: var(--text-primary);
}

.segmented__option:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}

.segmented__option:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.segmented__icon {
  font-size: var(--icon-md);
}
</style>
