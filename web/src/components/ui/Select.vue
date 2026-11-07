<script setup lang="ts" generic="T extends string | number">
import { nextTick, useId } from 'vue'

import Icon from './Icon.vue'

/** A native <select> (keyboard and screen-reader behaviour for free) styled with tokens. */
export interface SelectOption<V> {
  value: V
  label: string
  disabled?: boolean
}

const model = defineModel<T>({ required: true })

withDefaults(
  defineProps<{
    label: string
    hideLabel?: boolean
    options: readonly SelectOption<T>[]
    hint?: string
    disabled?: boolean
    size?: 'sm' | 'md'
  }>(),
  { hint: undefined, size: 'md' },
)

const id = useId()

async function onChange(event: Event, options: readonly SelectOption<T>[]) {
  const el = event.target as HTMLSelectElement
  const option = options[el.selectedIndex]
  if (option) model.value = option.value
  // A parent that refused the change (it asks first, say) keeps its value: show that option again.
  await nextTick()
  const kept = options.findIndex((o) => o.value === model.value)
  if (kept >= 0 && el.selectedIndex !== kept) el.selectedIndex = kept
}
</script>

<template>
  <div class="select" :class="`select--${size}`">
    <label :for="id" class="select__label" :class="{ 'sr-only': hideLabel }">{{ label }}</label>
    <div class="select__control">
      <select
        :id="id"
        class="select__input"
        :disabled="disabled"
        :aria-describedby="hint ? `${id}-hint` : undefined"
        @change="onChange($event, options)"
      >
        <option
          v-for="option in options"
          :key="String(option.value)"
          :value="String(option.value)"
          :selected="option.value === model"
          :disabled="option.disabled"
        >
          {{ option.label }}
        </option>
      </select>
      <Icon name="chevronDown" class="select__chevron" />
    </div>
    <p v-if="hint" :id="`${id}-hint`" class="select__hint">{{ hint }}</p>
  </div>
</template>

<style scoped>
/* Positioned so the hidden label and the chevron stay inside a scrolling page (they would stretch the document). */
.select {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.select__label {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.select__control {
  position: relative;
  display: flex;
  align-items: center;
}

.select__input {
  width: 100%;
  min-height: max(var(--control-md), var(--target-min));
  padding: 0 calc(var(--space-3) + var(--icon-md) + var(--space-1)) 0 var(--space-3);
  border: var(--border-width) solid var(--border-strong);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
  color: var(--text-primary);
  font-size: var(--fs-sm);
  appearance: none;
  cursor: pointer;
  text-overflow: ellipsis;
}

.select--sm .select__input {
  min-height: max(var(--control-sm), var(--target-min));
}

.select__input:hover {
  border-color: var(--text-muted);
}

.select__input:focus-visible {
  outline: none;
  border-color: var(--border-focus);
  box-shadow: 0 0 0 1px var(--border-focus);
}

.select__input:disabled {
  cursor: not-allowed;
  opacity: 0.6;
}

.select__input option {
  background: var(--bg-elevated);
  color: var(--text-primary);
}

.select__chevron {
  position: absolute;
  right: var(--space-3);
  color: var(--text-muted);
  font-size: var(--icon-sm);
  pointer-events: none;
}

.select__hint {
  color: var(--text-muted);
  font-size: var(--fs-xs);
}
</style>
