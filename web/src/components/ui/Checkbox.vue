<script setup lang="ts">
import { ref, useId, watchEffect } from 'vue'

import Icon from './Icon.vue'

const model = defineModel<boolean>({ default: false })

const props = withDefaults(
  defineProps<{
    label: string
    hideLabel?: boolean
    description?: string
    indeterminate?: boolean
    disabled?: boolean
  }>(),
  { description: undefined },
)

const id = useId()
const input = ref<HTMLInputElement | null>(null)

watchEffect(() => {
  if (input.value) input.value.indeterminate = props.indeterminate
})
</script>

<template>
  <div class="check" :class="{ 'check--disabled': disabled }">
    <span class="check__box">
      <input
        :id="id"
        ref="input"
        v-model="model"
        type="checkbox"
        class="check__input"
        :disabled="disabled"
        :aria-label="hideLabel ? label : undefined"
        :aria-describedby="description ? `${id}-desc` : undefined"
      />
      <Icon v-if="indeterminate" name="minus" class="check__mark" :stroke-width="3" />
      <Icon v-else name="check" class="check__mark" :stroke-width="3" />
    </span>
    <span v-if="!hideLabel" class="check__text">
      <label :for="id" class="check__label">{{ label }}</label>
      <span v-if="description" :id="`${id}-desc`" class="check__desc">{{ description }}</span>
    </span>
  </div>
</template>

<style scoped>
.check {
  display: inline-flex;
  align-items: flex-start;
  gap: var(--space-2);
}

.check__box {
  position: relative;
  display: inline-flex;
  flex: none;
  width: 18px;
  height: 18px;
  margin: 1px 0;
}

.check__input {
  width: 100%;
  height: 100%;
  margin: 0;
  border: var(--border-width) solid var(--border-strong);
  border-radius: var(--radius-xs);
  background: var(--bg-surface);
  appearance: none;
  cursor: pointer;
  transition:
    background-color var(--transition-fast),
    border-color var(--transition-fast);
}

.check__input:hover {
  border-color: var(--text-muted);
}

.check__input:checked,
.check__input:indeterminate {
  border-color: var(--accent);
  background: var(--accent);
}

.check__input:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}

.check__mark {
  position: absolute;
  inset: 3px;
  width: 12px;
  height: 12px;
  color: var(--text-on-accent);
  opacity: 0;
  pointer-events: none;
}

.check__input:checked + .check__mark,
.check__input:indeterminate + .check__mark {
  opacity: 1;
}

.check--disabled {
  opacity: 0.5;
}

.check__text {
  display: flex;
  flex-direction: column;
  gap: var(--space-0);
}

.check__label {
  color: var(--text-primary);
  font-size: var(--fs-sm);
  cursor: pointer;
}

.check__desc {
  color: var(--text-muted);
  font-size: var(--fs-xs);
}
</style>
