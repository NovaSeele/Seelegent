<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import FileIcon from '@/components/ui/FileIcon.vue'
import Icon from '@/components/ui/Icon.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useFormat } from '@/composables/useFormat'
import { useNow } from '@/composables/useNow'
import { relativeTo } from '@/lib/paths'

import type { Step } from './process'
import StageTag from './StageTag.vue'

/** One step of the Tree. Clicking it shows the step in the Terminal. */
const props = defineProps<{ step: Step; cwd: string }>()
defineEmits<{ reveal: [key: string] }>()

const { t } = useI18n()
const format = useFormat()
// Only a running step needs the clock (its duration grows); finished rows never re-render on a tick.
const now = props.step.state === 'running' ? useNow(1000) : null

const label = computed(() =>
  props.step.path && props.step.label === props.step.path
    ? relativeTo(props.step.path, props.cwd)
    : props.step.label,
)
const duration = computed(() => {
  const step = props.step
  if (step.state === 'running' && now) return Math.max(0, now.value - step.start)
  return step.durationMs
})
</script>

<template>
  <li class="sr" :class="`sr--${step.state}`">
    <button
      type="button"
      class="sr__btn"
      data-step
      :title="t('process.reveal')"
      @click="$emit('reveal', step.key)"
    >
      <Spinner v-if="step.state === 'running'" size="sm" decorative />
      <Icon v-else :name="step.state === 'failed' ? 'statusFailed' : 'check'" class="sr__state" />
      <span class="sr-only">{{ t(`process.state.${step.state}`) }}</span>
      <StageTag :stage="step.stage" class="sr__stage" />
      <span class="sr__name">{{ step.name }}</span>
      <FileIcon v-if="step.path" :path="step.path" />
      <code class="sr__label">{{ label }}</code>
      <span v-if="duration" class="sr__time">{{ format.duration(duration) }}</span>
    </button>
  </li>
</template>

<style scoped>
.sr {
  list-style: none;
}

.sr__btn {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  min-height: var(--control-sm);
  padding: 0 var(--space-2);
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-primary);
  font: inherit;
  font-size: var(--fs-xs);
  text-align: left;
  cursor: pointer;
}

.sr__btn:hover {
  background: var(--bg-hover);
  --text-muted: var(--text-primary);
  --text-secondary: var(--text-primary);
}

.sr__btn:focus-visible {
  outline: none;
  box-shadow: inset 0 0 0 var(--focus-ring-width) var(--border-focus);
}

.sr__state {
  flex: none;
  font-size: var(--icon-sm);
  color: var(--status-accepted-fg);
}

.sr--failed .sr__state {
  color: var(--status-danger-fg);
}

.sr__stage {
  width: 5.5em;
}

.sr__name {
  flex: none;
  color: var(--text-secondary);
}

.sr__label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  font-family: var(--font-mono);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sr--failed .sr__label {
  color: var(--status-danger-fg);
}

.sr__time {
  flex: none;
  color: var(--text-muted);
  font-variant-numeric: tabular-nums;
}

@media (width < 600px) {
  .sr__stage,
  .sr__name {
    display: none;
  }
}
</style>
