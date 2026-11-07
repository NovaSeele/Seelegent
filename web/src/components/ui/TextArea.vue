<script setup lang="ts">
import { computed, useId } from 'vue'

/** A multi-line text field with a label, hint or error, and an optional character limit shown as "n / max". */
const model = defineModel<string>({ default: '' })

const props = withDefaults(
  defineProps<{
    label: string
    hideLabel?: boolean
    hint?: string
    error?: string
    rows?: number
    maxlength?: number
    placeholder?: string
    mono?: boolean
    disabled?: boolean
  }>(),
  { hint: undefined, error: undefined, rows: 4, maxlength: undefined, placeholder: undefined },
)

const id = useId()
const describedBy = computed(
  () =>
    [props.error ? `${id}-error` : null, props.hint ? `${id}-hint` : null]
      .filter(Boolean)
      .join(' ') || undefined,
)
</script>

<template>
  <div class="area" :class="{ 'area--invalid': error }">
    <div class="area__top">
      <label :for="id" class="area__label" :class="{ 'sr-only': hideLabel }">{{ label }}</label>
      <span v-if="maxlength" class="area__count" aria-hidden="true">
        {{ model.length }} / {{ maxlength }}
      </span>
    </div>
    <textarea
      :id="id"
      v-model="model"
      class="area__input"
      :class="{ 'area__input--mono': mono }"
      :rows="rows"
      :maxlength="maxlength"
      :placeholder="placeholder"
      :disabled="disabled"
      :aria-invalid="error ? 'true' : undefined"
      :aria-describedby="describedBy"
      spellcheck="false"
    />
    <p v-if="error" :id="`${id}-error`" class="area__error">{{ error }}</p>
    <p v-else-if="hint" :id="`${id}-hint`" class="area__hint">{{ hint }}</p>
  </div>
</template>

<style scoped>
.area {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.area__top {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-2);
}

.area__label {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.area__count {
  color: var(--text-muted);
  font-size: var(--fs-2xs);
  font-variant-numeric: tabular-nums;
}

.area__input {
  width: 100%;
  min-height: calc(var(--control-md) * 2);
  padding: var(--space-2) var(--space-3);
  border: var(--border-width) solid var(--border-strong);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
  color: var(--text-primary);
  font: inherit;
  font-size: var(--fs-sm);
  line-height: var(--lh-prose);
  resize: vertical;
}

.area__input--mono {
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
}

.area__input:hover {
  border-color: var(--text-muted);
}

.area__input:focus {
  border-color: var(--border-focus);
  outline: none;
  box-shadow: 0 0 0 1px var(--border-focus);
}

.area--invalid .area__input {
  border-color: var(--status-danger-fg);
}

.area__input::placeholder {
  color: var(--text-muted);
}

.area__hint,
.area__error {
  color: var(--text-muted);
  font-size: var(--fs-xs);
}

.area__error {
  color: var(--status-danger-fg);
}
</style>
