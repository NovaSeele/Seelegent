<script setup lang="ts" generic="T extends string">
import { useId } from 'vue'

export interface Choice<V> {
  value: V
  label: string
  description?: string
  disabled?: boolean
}

/**
 * Radio buttons drawn as cards (a fieldset with a legend): arrow keys move between them, as with any radio group.
 */
const model = defineModel<T>({ required: true })
defineProps<{ legend: string; options: readonly Choice<T>[] }>()

const name = useId()
</script>

<template>
  <fieldset class="choices">
    <legend class="choices__legend">{{ legend }}</legend>
    <div class="choices__list">
      <label
        v-for="option in options"
        :key="option.value"
        class="choice"
        :class="{ 'choice--on': model === option.value, 'choice--off': option.disabled }"
      >
        <input
          v-model="model"
          class="choice__input"
          type="radio"
          :name="name"
          :value="option.value"
          :disabled="option.disabled"
        />
        <span class="choice__text">
          <span class="choice__label">{{ option.label }}</span>
          <span v-if="option.description" class="choice__desc">{{ option.description }}</span>
        </span>
      </label>
    </div>
  </fieldset>
</template>

<style scoped>
.choices {
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}

.choices__legend {
  margin-bottom: var(--space-2);
  padding: 0;
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.choices__list {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: var(--space-2);
}

.choice {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  padding: var(--space-3);
  border: var(--border-width) solid var(--border-strong);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
  cursor: pointer;
}

.choice:hover {
  border-color: var(--text-muted);
}

.choice--on {
  border-color: var(--accent);
  background: var(--accent-soft);
}

.choice--off {
  cursor: not-allowed;
  opacity: 0.6;
}

.choice:has(.choice__input:focus-visible) {
  outline: 2px solid var(--border-focus);
  outline-offset: 2px;
}

.choice__input {
  flex: none;
  margin: 2px 0 0;
  accent-color: var(--accent);
}

.choice__text {
  display: flex;
  flex-direction: column;
  gap: var(--space-0);
  min-width: 0;
}

.choice__label {
  font-size: var(--fs-sm);
  font-weight: var(--fw-medium);
}

.choice__desc {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}
</style>
