<script setup lang="ts">
import { useId } from 'vue'

/** A native range slider with its current value shown next to it (keyboard: arrows, Home, End, PageUp/Down). */
const model = defineModel<number>({ required: true })

defineProps<{
  label: string
  range: { readonly min: number; readonly max: number; readonly step: number }
  format: (value: number) => string
}>()

const id = useId()
</script>

<template>
  <div class="range">
    <input
      :id="id"
      v-model.number="model"
      class="range__input"
      type="range"
      :min="range.min"
      :max="range.max"
      :step="range.step"
      :aria-label="label"
      :aria-valuetext="format(model)"
    />
    <output class="range__value" :for="id">{{ format(model) }}</output>
  </div>
</template>

<style scoped>
.range {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.range__input {
  width: 180px;
  height: var(--control-md);
  margin: 0;
  accent-color: var(--accent);
  cursor: pointer;
}

.range__value {
  min-width: 7ch;
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  font-variant-numeric: tabular-nums;
  text-align: end;
}
</style>
