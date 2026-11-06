<script setup lang="ts">
import { computed } from 'vue'

import { ICONS, type IconName } from './icons'

const props = withDefaults(
  defineProps<{
    name: IconName
    /** Pixel size or a CSS length; defaults to 1em so it follows the text. */
    size?: number | string
    /** Accessible name; without it the icon is decorative (aria-hidden). */
    label?: string
    strokeWidth?: number
  }>(),
  { size: '1em', label: undefined, strokeWidth: 2 },
)

const shapes = computed(() => ICONS[props.name])
const dimension = computed(() => (typeof props.size === 'number' ? `${props.size}px` : props.size))
</script>

<template>
  <svg
    class="icon"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    :stroke-width="strokeWidth"
    stroke-linecap="round"
    stroke-linejoin="round"
    :width="dimension"
    :height="dimension"
    :role="label ? 'img' : undefined"
    :aria-label="label"
    :aria-hidden="label ? undefined : 'true'"
    focusable="false"
  >
    <template v-for="(shape, index) in shapes" :key="index">
      <path v-if="shape[0] === 'path'" :d="shape[1]" />
      <circle v-else-if="shape[0] === 'circle'" :cx="shape[1]" :cy="shape[2]" :r="shape[3]" />
      <circle
        v-else-if="shape[0] === 'ring'"
        :cx="shape[1]"
        :cy="shape[2]"
        :r="shape[3]"
        stroke-dasharray="3.5 3.5"
      />
      <circle
        v-else-if="shape[0] === 'dot'"
        :cx="shape[1]"
        :cy="shape[2]"
        :r="shape[3]"
        fill="currentColor"
        stroke="none"
      />
      <rect
        v-else-if="shape[0] === 'rect'"
        :x="shape[1]"
        :y="shape[2]"
        :width="shape[3]"
        :height="shape[4]"
        :rx="shape[5]"
      />
      <line
        v-else-if="shape[0] === 'line'"
        :x1="shape[1]"
        :y1="shape[2]"
        :x2="shape[3]"
        :y2="shape[4]"
      />
      <polyline v-else-if="shape[0] === 'polyline'" :points="shape[1]" />
      <polyline v-else-if="shape[0] === 'knock'" class="icon__knock" :points="shape[1]" />
    </template>
  </svg>
</template>

<style scoped>
.icon {
  flex: none;
  display: inline-block;
  vertical-align: middle;
}

.icon__knock {
  stroke: var(--icon-knockout, var(--bg-surface));
}
</style>
