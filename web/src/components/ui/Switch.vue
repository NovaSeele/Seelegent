<script setup lang="ts">
import { useId } from 'vue'

const model = defineModel<boolean>({ default: false })

withDefaults(
  defineProps<{
    label: string
    hideLabel?: boolean
    description?: string
    disabled?: boolean
  }>(),
  { description: undefined },
)

const id = useId()
</script>

<template>
  <div class="switch" :class="{ 'switch--disabled': disabled }">
    <button
      :id="id"
      type="button"
      role="switch"
      class="switch__track"
      :aria-checked="model"
      :aria-describedby="description ? `${id}-desc` : undefined"
      :aria-label="hideLabel ? label : undefined"
      :aria-labelledby="hideLabel ? undefined : `${id}-label`"
      :disabled="disabled"
      @click="model = !model"
    >
      <span class="switch__thumb" />
    </button>
    <span v-if="!hideLabel" class="switch__text">
      <label :id="`${id}-label`" :for="id" class="switch__label">{{ label }}</label>
      <span v-if="description" :id="`${id}-desc`" class="switch__desc">{{ description }}</span>
    </span>
  </div>
</template>

<style scoped>
.switch {
  display: inline-flex;
  align-items: flex-start;
  gap: var(--space-2);
}

.switch__track {
  position: relative;
  flex: none;
  width: 34px;
  height: 20px;
  margin: var(--space-0) 0;
  padding: 0;
  border: var(--border-width) solid var(--border-strong);
  border-radius: var(--radius-pill);
  background: var(--bg-surface-2);
  cursor: pointer;
  transition:
    background-color var(--transition-fast),
    border-color var(--transition-fast);
}

.switch__track::before {
  /* Touch: a 44px hit area around the visible track. */
  content: '';
  position: absolute;
  inset: calc((min(var(--target-min), 44px) - 20px) / -2) 0;
}

.switch__thumb {
  position: absolute;
  top: 2px;
  left: 2px;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: var(--text-muted);
  transition:
    transform var(--duration-base) var(--ease-standard),
    background-color var(--transition-fast);
}

.switch__track[aria-checked='true'] {
  border-color: var(--accent);
  background: var(--accent);
}

.switch__track[aria-checked='true'] .switch__thumb {
  background: var(--text-on-accent);
  transform: translateX(14px);
}

.switch__track:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}

.switch--disabled {
  opacity: 0.5;
}

.switch--disabled .switch__track {
  cursor: not-allowed;
}

.switch__text {
  display: flex;
  flex-direction: column;
  gap: var(--space-0);
}

.switch__label {
  color: var(--text-primary);
  font-size: var(--fs-sm);
  cursor: pointer;
}

.switch__desc {
  color: var(--text-muted);
  font-size: var(--fs-xs);
}
</style>
