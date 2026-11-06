<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import Icon from './Icon.vue'
import type { IconName } from './icons'
import type { Tone } from './tone'

/**
 * A compact label: neutral facts (backend, role, stage), filters, counts. `selected` makes it a toggle
 * (aria-pressed); `removable` adds a remove button.
 */
const props = withDefaults(
  defineProps<{
    tone?: Tone
    icon?: IconName
    /** When set, the chip is a toggle button. */
    selected?: boolean
    removable?: boolean
  }>(),
  { tone: 'neutral', icon: undefined, selected: undefined },
)

const emit = defineEmits<{ toggle: []; remove: [] }>()

const { t } = useI18n()

function onClick() {
  if (props.selected !== undefined) emit('toggle')
}
</script>

<template>
  <component
    :is="selected === undefined ? 'span' : 'button'"
    :type="selected === undefined ? undefined : 'button'"
    class="chip"
    :class="[`tone-${tone}`, { 'chip--toggle': selected !== undefined, 'is-selected': selected }]"
    :aria-pressed="selected === undefined ? undefined : selected"
    @click="onClick"
  >
    <Icon v-if="icon" :name="icon" class="chip__icon" />
    <span class="chip__label"><slot /></span>
    <button
      v-if="removable"
      type="button"
      class="chip__remove"
      :aria-label="t('common.action.remove')"
      @click.stop="emit('remove')"
    >
      <Icon name="x" />
    </button>
  </component>
</template>

<style scoped>
.chip {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  max-width: 100%;
  height: 24px;
  padding: 0 var(--space-2);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-surface-2);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  line-height: 1;
  white-space: nowrap;
}

.chip:not(.tone-neutral) {
  border-color: var(--tone-border);
  background: var(--tone-bg);
  color: var(--tone-fg);
}

.chip--toggle {
  cursor: pointer;
  min-height: max(24px, var(--target-min));
}

.chip--toggle:hover {
  border-color: var(--border-strong);
  color: var(--text-primary);
}

.chip--toggle.is-selected {
  border-color: var(--accent-border);
  background: var(--accent-soft);
  color: var(--accent);
}

.chip:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}

.chip__icon {
  font-size: var(--icon-sm);
}

.chip__label {
  overflow: hidden;
  text-overflow: ellipsis;
}

.chip__remove {
  display: inline-flex;
  margin-right: calc(-1 * var(--space-1));
  padding: var(--space-0);
  border: 0;
  border-radius: var(--radius-xs);
  background: transparent;
  color: inherit;
  cursor: pointer;
}

.chip__remove:hover {
  background: var(--bg-hover);
}
</style>
