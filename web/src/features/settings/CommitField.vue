<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'

/**
 * A text setting that saves when you are done with it: on blur, Enter (Ctrl+Enter when multi-line) commits, Esc
 * puts the saved value back. While it has focus, a new value from the daemon does not overwrite the typing.
 */
const props = withDefaults(
  defineProps<{
    modelValue: string
    label: string
    hideLabel?: boolean
    multiline?: boolean
    placeholder?: string
    hint?: string
    error?: string
    disabled?: boolean
  }>(),
  { placeholder: undefined, hint: undefined, error: undefined },
)

/** `commit`: a new value to save. `unchanged`: the field shows the saved value again (typed back, Esc). */
const emit = defineEmits<{ commit: [value: string]; unchanged: [] }>()

const id = useId()
const text = ref(props.modelValue)
let focused = false

watch(
  () => props.modelValue,
  (value) => {
    if (!focused) text.value = value
  },
)

const describedBy = computed(
  () =>
    [props.error ? `${id}-error` : null, props.hint ? `${id}-hint` : null]
      .filter(Boolean)
      .join(' ') || undefined,
)

/** The text last committed since the last keystroke: Enter then blur sends a refused value only once. */
let sent: string | undefined
watch([text, () => props.modelValue], () => (sent = undefined))

function commit() {
  if (text.value === props.modelValue) {
    emit('unchanged')
    return
  }
  if (text.value === sent) return
  sent = text.value
  emit('commit', text.value)
}

function onBlur() {
  focused = false
  commit()
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Enter' && (!props.multiline || event.ctrlKey || event.metaKey)) {
    event.preventDefault()
    commit()
  } else if (event.key === 'Escape' && text.value !== props.modelValue) {
    event.stopPropagation()
    text.value = props.modelValue
    emit('unchanged')
  }
}
</script>

<template>
  <div class="cf" :class="{ 'cf--invalid': error }">
    <label :for="id" class="cf__label" :class="{ 'sr-only': hideLabel }">{{ label }}</label>
    <!-- Two plain elements, not <component :is>: v-model on a dynamic component binds a modelValue prop, which a
         native input ignores (the field showed empty and typing never reached `text`). -->
    <textarea
      v-if="multiline"
      :id="id"
      v-model="text"
      class="cf__input cf__input--multi"
      rows="3"
      :placeholder="placeholder"
      :disabled="disabled"
      :aria-invalid="error ? 'true' : undefined"
      :aria-describedby="describedBy"
      spellcheck="false"
      autocomplete="off"
      @focus="focused = true"
      @blur="onBlur"
      @keydown="onKeydown"
    />
    <input
      v-else
      :id="id"
      v-model="text"
      class="cf__input"
      type="text"
      :placeholder="placeholder"
      :disabled="disabled"
      :aria-invalid="error ? 'true' : undefined"
      :aria-describedby="describedBy"
      spellcheck="false"
      autocomplete="off"
      @focus="focused = true"
      @blur="onBlur"
      @keydown="onKeydown"
    />
    <p v-if="error" :id="`${id}-error`" class="cf__error">{{ error }}</p>
    <p v-else-if="hint" :id="`${id}-hint`" class="cf__hint">{{ hint }}</p>
  </div>
</template>

<style scoped>
.cf {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.cf__label {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.cf__input {
  width: 100%;
  min-height: max(var(--control-sm), var(--target-min));
  padding: 0 var(--space-2);
  border: var(--border-width) solid var(--border-strong);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
}

.cf__input--multi {
  padding: var(--space-2);
  resize: vertical;
}

.cf__input:hover {
  border-color: var(--text-muted);
}

.cf__input:focus {
  border-color: var(--border-focus);
  outline: none;
  box-shadow: 0 0 0 1px var(--border-focus);
}

.cf--invalid .cf__input {
  border-color: var(--status-danger-fg);
}

.cf__hint,
.cf__error {
  color: var(--text-muted);
  font-size: var(--fs-xs);
}

.cf__error {
  color: var(--status-danger-fg);
}
</style>
