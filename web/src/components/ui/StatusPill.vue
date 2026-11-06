<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import Icon from './Icon.vue'
import type { IconName } from './icons'
import { TASK_STATUS, type TaskStatus, type Tone } from './tone'

/**
 * A status shown as icon + word + colour. Pass `status` for a task state (label and tone come from the shared
 * vocabulary), or `tone` + `label` (+ `icon`) for anything else.
 */
const props = withDefaults(
  defineProps<{
    status?: TaskStatus
    tone?: Tone
    label?: string
    icon?: IconName
    /** Use the short word (common.status.<x>.short) in tight places. */
    short?: boolean
    size?: 'sm' | 'md'
  }>(),
  { status: undefined, tone: 'neutral', label: undefined, icon: undefined, size: 'md' },
)

const { t } = useI18n()

const view = computed(() => {
  if (props.status) {
    const def = TASK_STATUS[props.status]
    const word = t(`common.status.${props.status}.${props.short ? 'short' : 'label'}`)
    return { tone: def.tone, icon: props.icon ?? def.icon, label: props.label ?? word }
  }
  return { tone: props.tone, icon: props.icon, label: props.label ?? '' }
})
</script>

<template>
  <span class="pill" :class="[`tone-${view.tone}`, `pill--${size}`]">
    <Icon v-if="view.icon" :name="view.icon" class="pill__icon" />
    <span class="pill__label">{{ view.label }}</span>
  </span>
</template>

<style scoped>
.pill {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  max-width: 100%;
  height: 22px;
  padding: 0 var(--space-2);
  border: var(--border-width) solid var(--tone-border);
  border-radius: var(--radius-pill);
  background: var(--tone-bg);
  color: var(--tone-fg);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
  line-height: 1;
  white-space: nowrap;
}

.pill--sm {
  height: 18px;
  padding: 0 var(--space-1);
  font-size: var(--fs-2xs);
}

.pill__icon {
  font-size: var(--icon-sm);
  --icon-knockout: var(--bg-surface);
}

.pill__label {
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
