<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'

/**
 * A whole or decimal number input with an optional unit after it. Typing does not commit: `commit` fires with the
 * parsed value when the field loses focus or Enter is pressed (or after `debounce` ms of no typing), so an
 * autosaving form does not send every keystroke. Esc puts the last committed value back. An empty or unparsable
 * entry commits nothing and shows the value again. An `integer` field rounds the entry and shows what it sent.
 * When the field's error goes away while it still holds an entry that was not saved (another field fixed the clash
 * that refused it), the entry commits again: it may be valid now, and if not its error comes back.
 */
const props = withDefaults(
  defineProps<{
    modelValue: number
    label: string
    hideLabel?: boolean
    unit?: string
    hint?: string
    error?: string
    size?: 'sm' | 'md'
    disabled?: boolean
    /** Commit after this many ms without typing (0 = only on blur and Enter). */
    debounce?: number
    /** Whole numbers only: the entry is rounded before it commits. */
    integer?: boolean
    /** The label sits before the input on the same row (a compact field in a toolbar or card footer). */
    inline?: boolean
    /** Ids of more text about the field outside it (notes under a setting), read after the error and hint. */
    describedby?: string
  }>(),
  {
    unit: undefined,
    hint: undefined,
    error: undefined,
    describedby: undefined,
    size: 'md',
    debounce: 0,
  },
)

/** `commit`: a new value to save. `unchanged`: the field shows the saved value again (typed back, Esc, unparsable). */
const emit = defineEmits<{ commit: [value: number]; unchanged: [] }>()

const id = useId()
const text = ref(String(props.modelValue))
let editing = false
let timer: ReturnType<typeof setTimeout> | undefined
/** The value last committed since the last keystroke: Enter then blur sends a refused value only once. */
let sent: number | undefined

watch(
  () => props.modelValue,
  (value) => {
    sent = undefined
    if (!editing) text.value = String(value)
  },
)

watch(
  () => props.error,
  (now, before) => {
    if (before && !now && text.value.trim() !== String(props.modelValue)) {
      sent = undefined
      commit()
    }
  },
)

const describedBy = computed(
  () =>
    [
      props.error ? `${id}-error` : null,
      props.hint ? `${id}-hint` : null,
      props.describedby ?? null,
    ]
      .filter(Boolean)
      .join(' ') || undefined,
)

function commit() {
  clearTimeout(timer)
  const raw = text.value.trim().replace(',', '.')
  const parsed = Number(raw)
  if (raw === '' || !Number.isFinite(parsed)) {
    text.value = String(props.modelValue)
    emit('unchanged')
    return
  }
  // A whole-number field shows the rounded value it commits, so a later blur finds nothing new to send.
  const value = props.integer ? Math.round(parsed) : parsed
  if (props.integer) text.value = String(value)
  if (value === props.modelValue) {
    sent = undefined
    emit('unchanged')
  } else if (value !== sent) {
    sent = value
    emit('commit', value)
  }
}

function onInput() {
  editing = true
  sent = undefined
  clearTimeout(timer)
  if (props.debounce > 0) timer = setTimeout(commit, props.debounce)
}

function onBlur() {
  commit()
  editing = false
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Enter') {
    event.preventDefault()
    commit()
  } else if (event.key === 'Escape' && text.value !== String(props.modelValue)) {
    event.stopPropagation()
    clearTimeout(timer)
    text.value = String(props.modelValue)
    sent = undefined
    emit('unchanged')
  }
}
</script>

<template>
  <div class="num" :class="[`num--${size}`, { 'num--invalid': error, 'num--inline': inline }]">
    <label :for="id" class="num__label" :class="{ 'sr-only': hideLabel }">{{ label }}</label>
    <div class="num__control">
      <input
        :id="id"
        v-model="text"
        class="num__input"
        type="text"
        inputmode="decimal"
        :disabled="disabled"
        :aria-invalid="error ? 'true' : undefined"
        :aria-describedby="describedBy"
        autocomplete="off"
        spellcheck="false"
        @input="onInput"
        @blur="onBlur"
        @keydown="onKeydown"
      />
      <span v-if="unit" class="num__unit" aria-hidden="true">{{ unit }}</span>
    </div>
    <p v-if="error" :id="`${id}-error`" class="num__error">{{ error }}</p>
    <p v-else-if="hint" :id="`${id}-hint`" class="num__hint">{{ hint }}</p>
  </div>
</template>

<style scoped>
.num {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.num__label {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.num__control {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  min-height: max(var(--control-md), var(--target-min));
  padding: 0 var(--space-3);
  border: var(--border-width) solid var(--border-strong);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
  transition: border-color var(--transition-fast);
}

.num--sm .num__control {
  min-height: max(var(--control-sm), var(--target-min));
  padding: 0 var(--space-2);
}

.num__control:hover {
  border-color: var(--text-muted);
}

.num__control:focus-within {
  border-color: var(--border-focus);
  box-shadow: 0 0 0 1px var(--border-focus);
}

.num--invalid .num__control {
  border-color: var(--status-danger-fg);
}

.num__input {
  flex: 1;
  width: 100%;
  min-width: 3ch;
  padding: 0;
  border: 0;
  outline: none;
  background: transparent;
  color: var(--text-primary);
  font-size: var(--fs-sm);
  font-variant-numeric: tabular-nums;
}

.num__input:disabled {
  cursor: not-allowed;
  opacity: 0.6;
}

.num__unit {
  flex: none;
  color: var(--text-muted);
  font-size: var(--fs-xs);
}

.num__hint,
.num__error {
  color: var(--text-muted);
  font-size: var(--fs-xs);
}

.num__error {
  color: var(--status-danger-fg);
}

.num--inline {
  flex-direction: row;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.num--inline .num__label {
  color: var(--text-primary);
  font-size: var(--fs-sm);
  font-weight: var(--fw-regular);
}

.num--inline .num__control {
  flex: none;
  width: var(--num-inline-width, 72px);
}

.num--inline .num__hint,
.num--inline .num__error {
  flex-basis: 100%;
}
</style>
