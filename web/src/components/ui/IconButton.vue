<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, type RouteLocationRaw } from 'vue-router'

import { ariaKeyShortcuts } from '@/lib/keys'

import type { Placement } from '@/composables/useFloating'

import Icon from './Icon.vue'
import type { IconName } from './icons'
import Tooltip from './Tooltip.vue'

defineOptions({ inheritAttrs: false })

/**
 * A square icon-only button. `label` is its accessible name and, by default, its tooltip. Other attributes
 * (aria-expanded, aria-controls, listeners) go to the button itself, not the tooltip wrapper.
 */
const props = withDefaults(
  defineProps<{
    icon: IconName
    label: string
    size?: 'sm' | 'md' | 'lg'
    variant?: 'ghost' | 'secondary'
    /** Shortcut shown in the tooltip and exposed as aria-keyshortcuts. */
    shortcut?: string
    tooltip?: boolean
    tooltipPlacement?: Placement
    pressed?: boolean
    disabled?: boolean
    to?: RouteLocationRaw
  }>(),
  {
    size: 'md',
    variant: 'ghost',
    shortcut: undefined,
    tooltip: true,
    tooltipPlacement: 'bottom',
    pressed: undefined,
    to: undefined,
  },
)

defineEmits<{ click: [event: MouseEvent] }>()

const keyshortcuts = computed(() => (props.shortcut ? ariaKeyShortcuts(props.shortcut) : undefined))
</script>

<template>
  <Tooltip :text="label" :shortcut="shortcut" :placement="tooltipPlacement" :disabled="!tooltip">
    <RouterLink
      v-if="to"
      :to="to"
      class="icon-btn"
      :class="[`icon-btn--${size}`, `icon-btn--${variant}`]"
      :aria-label="label"
      :aria-keyshortcuts="keyshortcuts"
      v-bind="$attrs"
    >
      <Icon :name="icon" />
    </RouterLink>
    <button
      v-else
      type="button"
      class="icon-btn"
      :class="[`icon-btn--${size}`, `icon-btn--${variant}`]"
      :aria-label="label"
      :aria-pressed="pressed"
      :aria-keyshortcuts="keyshortcuts"
      :disabled="disabled"
      v-bind="$attrs"
      @click="$emit('click', $event)"
    >
      <Icon :name="icon" />
    </button>
  </Tooltip>
</template>

<style scoped>
.icon-btn {
  --size: var(--control-md);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: max(var(--size), var(--target-min));
  height: max(var(--size), var(--target-min));
  padding: 0;
  border: var(--border-width) solid transparent;
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--text-secondary);
  font-size: var(--icon-md);
  cursor: pointer;
  transition:
    background-color var(--transition-fast),
    color var(--transition-fast);
}

.icon-btn--sm {
  --size: var(--control-sm);
  font-size: var(--icon-sm);
}

.icon-btn--lg {
  --size: var(--control-lg);
  font-size: var(--icon-lg);
}

.icon-btn--secondary {
  border-color: var(--border-strong);
  background: var(--bg-surface);
}

.icon-btn:hover:not(:disabled) {
  background: var(--bg-hover);
  --text-muted: var(--text-primary);
  --text-secondary: var(--text-primary);
  color: var(--text-primary);
}

.icon-btn[aria-pressed='true'] {
  background: var(--accent-soft);
  --text-muted: var(--text-primary);
  --text-secondary: var(--text-primary);
  color: var(--accent);
}

.icon-btn:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}

.icon-btn:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}
</style>
