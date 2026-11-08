<script setup lang="ts">
import { computed, ref, useId } from 'vue'

import Icon from './Icon.vue'
import type { IconName } from './icons'

const model = defineModel<string>({ default: '' })

const props = withDefaults(
  defineProps<{
    label: string
    /** Hide the label visually (it stays for screen readers), e.g. for a search box with an icon. */
    hideLabel?: boolean
    hint?: string
    error?: string
    type?: 'text' | 'search' | 'email' | 'url' | 'password' | 'number'
    placeholder?: string
    icon?: IconName
    size?: 'sm' | 'md'
    disabled?: boolean
    autocomplete?: string
    /** The on-screen keyboard to show (inputmode), e.g. numeric for a number that may also be pasted as a link. */
    inputmode?: 'text' | 'numeric' | 'url'
    /** Keyboard shortcut that focuses the field (aria-keyshortcuts). */
    keyshortcuts?: string
    /** Id of a <datalist> of suggestions (the field still takes any text). */
    list?: string
  }>(),
  {
    hint: undefined,
    error: undefined,
    type: 'text',
    placeholder: undefined,
    icon: undefined,
    size: 'md',
    autocomplete: 'off',
    inputmode: undefined,
    keyshortcuts: undefined,
    list: undefined,
  },
)

const id = useId()
const input = ref<HTMLInputElement | null>(null)
const describedBy = computed(
  () =>
    [props.error ? `${id}-error` : null, props.hint ? `${id}-hint` : null]
      .filter(Boolean)
      .join(' ') || undefined,
)

defineExpose({ focus: () => input.value?.focus(), select: () => input.value?.select() })
</script>

<template>
  <div class="field" :class="[`field--${size}`, { 'field--invalid': error }]">
    <label :for="id" class="field__label" :class="{ 'sr-only': hideLabel }">{{ label }}</label>
    <div class="field__control">
      <Icon v-if="icon" :name="icon" class="field__icon" />
      <input
        :id="id"
        ref="input"
        v-model="model"
        class="field__input"
        :type="type"
        :placeholder="placeholder"
        :disabled="disabled"
        :autocomplete="autocomplete"
        :inputmode="inputmode"
        :aria-invalid="error ? 'true' : undefined"
        :aria-describedby="describedBy"
        :aria-keyshortcuts="keyshortcuts"
        :list="list"
        spellcheck="false"
      />
      <slot name="end" />
    </div>
    <p v-if="error" :id="`${id}-error`" class="field__error">{{ error }}</p>
    <p v-else-if="hint" :id="`${id}-hint`" class="field__hint">{{ hint }}</p>
    <!-- A control that goes with the hint (e.g. a button that changes what it says): outside the described text,
         so screen readers do not read its label as part of the field's description. -->
    <slot name="hint-action" />
  </div>
</template>

<style scoped>
.field {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.field__label {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.field__control {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: max(var(--control-md), var(--target-min));
  padding: 0 var(--space-3);
  border: var(--border-width) solid var(--border-strong);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
  transition: border-color var(--transition-fast);
}

.field--sm .field__control {
  min-height: max(var(--control-sm), var(--target-min));
  padding: 0 var(--space-2);
}

.field__control:hover {
  border-color: var(--text-muted);
}

.field__control:focus-within {
  border-color: var(--border-focus);
  box-shadow: 0 0 0 1px var(--border-focus);
}

.field--invalid .field__control {
  border-color: var(--status-danger-fg);
}

.field__icon {
  flex: none;
  color: var(--text-muted);
  font-size: var(--icon-md);
}

.field__input {
  flex: 1;
  min-width: 0;
  height: 100%;
  padding: 0;
  border: 0;
  outline: none;
  background: transparent;
  color: var(--text-primary);
  font-size: var(--fs-sm);
}

.field__input::placeholder {
  color: var(--text-muted);
}

.field__input::-webkit-search-cancel-button {
  display: none;
}

.field__input:disabled {
  cursor: not-allowed;
  opacity: 0.6;
}

.field__hint,
.field__error {
  font-size: var(--fs-xs);
  color: var(--text-muted);
}

.field__error {
  color: var(--status-danger-fg);
}
</style>
