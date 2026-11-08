<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, type RouteLocationRaw } from 'vue-router'

import Icon from './Icon.vue'
import type { IconName } from './icons'
import Spinner from './Spinner.vue'

export type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger'
export type ButtonSize = 'sm' | 'md' | 'lg'

const props = withDefaults(
  defineProps<{
    variant?: ButtonVariant
    size?: ButtonSize
    icon?: IconName
    iconEnd?: IconName
    /** Shows a spinner and blocks clicks; the label stays for layout and screen readers. */
    loading?: boolean
    disabled?: boolean
    block?: boolean
    type?: 'button' | 'submit' | 'reset'
    /** Renders a router link styled as a button. */
    to?: RouteLocationRaw
    /** Renders a link to another site styled as a button, opened in a new tab (http(s) only; check it first). */
    href?: string
  }>(),
  {
    variant: 'secondary',
    size: 'md',
    icon: undefined,
    iconEnd: undefined,
    type: 'button',
    to: undefined,
    href: undefined,
  },
)

defineEmits<{ click: [event: MouseEvent] }>()

const classes = computed(() => [
  'btn',
  `btn--${props.variant}`,
  `btn--${props.size}`,
  { 'btn--block': props.block, 'btn--loading': props.loading },
])
</script>

<template>
  <RouterLink v-if="to && !disabled" :to="to" :class="classes">
    <Icon v-if="icon" :name="icon" class="btn__icon" />
    <span class="btn__label"><slot /></span>
    <Icon v-if="iconEnd" :name="iconEnd" class="btn__icon" />
  </RouterLink>
  <a
    v-else-if="href && !disabled"
    :href="href"
    target="_blank"
    rel="noopener noreferrer"
    :class="classes"
  >
    <Icon v-if="icon" :name="icon" class="btn__icon" />
    <span class="btn__label"><slot /></span>
    <Icon v-if="iconEnd" :name="iconEnd" class="btn__icon" />
  </a>
  <button
    v-else
    :type="type"
    :class="classes"
    :disabled="disabled || undefined"
    :aria-busy="loading || undefined"
    @click="loading ? $event.preventDefault() : $emit('click', $event)"
  >
    <Spinner v-if="loading" class="btn__icon" size="sm" decorative />
    <Icon v-else-if="icon" :name="icon" class="btn__icon" />
    <span class="btn__label"><slot /></span>
    <Icon v-if="iconEnd" :name="iconEnd" class="btn__icon" />
  </button>
</template>

<style scoped>
.btn {
  --btn-height: var(--control-md);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  min-height: max(var(--btn-height), var(--target-min));
  padding: 0 var(--space-3);
  border: var(--border-width) solid transparent;
  border-radius: var(--radius-md);
  font-size: var(--fs-sm);
  font-weight: var(--fw-medium);
  line-height: var(--lh-tight);
  text-decoration: none;
  white-space: nowrap;
  cursor: pointer;
  user-select: none;
  transition:
    background-color var(--transition-fast),
    border-color var(--transition-fast),
    color var(--transition-fast);
}

.btn:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}

.btn:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.btn--loading {
  cursor: progress;
}

.btn--sm {
  --btn-height: var(--control-sm);
  padding: 0 var(--space-2);
  gap: var(--space-1);
  font-size: var(--fs-xs);
}

.btn--lg {
  --btn-height: var(--control-lg);
  padding: 0 var(--space-4);
  font-size: var(--fs-md);
}

.btn--block {
  display: flex;
  width: 100%;
}

.btn__icon {
  font-size: var(--icon-md);
}

.btn--sm .btn__icon {
  font-size: var(--icon-sm);
}

.btn__label {
  overflow: hidden;
  text-overflow: ellipsis;
}

.btn__label:empty {
  display: none;
}

.btn--primary {
  background: var(--accent);
  color: var(--text-on-accent);
}

.btn--primary:hover:not(:disabled) {
  background: var(--accent-hover);
}

.btn--secondary {
  background: var(--bg-surface);
  border-color: var(--border-strong);
  color: var(--text-primary);
}

.btn--secondary:hover:not(:disabled) {
  background: var(--bg-hover);
  --text-muted: var(--text-primary);
  --text-secondary: var(--text-primary);
}

.btn--ghost {
  background: transparent;
  color: var(--text-secondary);
}

.btn--ghost:hover:not(:disabled) {
  background: var(--bg-hover);
  --text-muted: var(--text-primary);
  --text-secondary: var(--text-primary);
  color: var(--text-primary);
}

.btn--danger {
  background: var(--status-danger-bg);
  border-color: var(--status-danger-border);
  color: var(--status-danger-fg);
}

.btn--danger:hover:not(:disabled) {
  border-color: var(--status-danger-fg);
}

.btn:active:not(:disabled) {
  transform: translateY(1px);
}
</style>
