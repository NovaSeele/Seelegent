<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { TaskDetail, TaskSummary } from '@/api/types'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import { useFormat } from '@/composables/useFormat'
import { useNow } from '@/composables/useNow'
import { relativeTo } from '@/lib/paths'

import { CHART } from './graph'
import GraphChart from './GraphChart.vue'
import GraphDetail from './GraphDetail.vue'
import { STAGES, type Step } from './process'
import ProcessFrame from './ProcessFrame.vue'
import StageTag from './StageTag.vue'
import { useGraphLayout } from './useGraphLayout'
import { useProcess } from './useProcess'

/**
 * The Graph view: steps over time in one lane per stage, idle stretches cut short, a mark where each turn or
 * later attempt (a hand-over to another account) starts. Click a bar, or use Left/Right, to see a step;
 * Enter shows it in the Terminal.
 */
const props = defineProps<{ task: TaskSummary; detail: TaskDetail | null }>()
const emit = defineEmits<{ reveal: [key: string] }>()

const { t } = useI18n()
const format = useFormat()
const model = useProcess(props.task.id, () => props.detail)

const hasRunning = computed(() => model.steps.value.some((step) => step.state === 'running'))
const clock = useNow(1000)
const durationOf = (step: Step) =>
  step.state === 'running' && hasRunning.value
    ? Math.max(0, clock.value - step.start)
    : step.durationMs

const { zoom, scroller, layout } = useGraphLayout(model.turns, durationOf)
const zoomOptions = computed(() => [
  { value: 'fit' as const, label: t('process.graph.fit') },
  { value: '2' as const, label: '2×' },
  { value: '4' as const, label: '4×' },
])

const ordered = computed(() => layout.value.bars)
const selectedKey = ref<string | null>(null)
const selected = computed(
  () => ordered.value.find((bar) => bar.step.key === selectedKey.value)?.step ?? null,
)

function select(key: string) {
  selectedKey.value = key
  const bar = ordered.value.find((b) => b.step.key === key)
  const el = scroller.value
  if (!bar || !el) return
  if (bar.x < el.scrollLeft || bar.x + bar.w > el.scrollLeft + el.clientWidth) {
    el.scrollLeft = Math.max(0, bar.x - el.clientWidth / 3)
  }
}

function onKeydown(event: KeyboardEvent) {
  const bars = ordered.value
  if (!bars.length) return
  const index = bars.findIndex((bar) => bar.step.key === selectedKey.value)
  let next: number | null = null
  if (event.key === 'ArrowRight') next = Math.min(bars.length - 1, index + 1)
  else if (event.key === 'ArrowLeft') next = Math.max(0, index < 0 ? 0 : index - 1)
  else if (event.key === 'Home') next = 0
  else if (event.key === 'End') next = bars.length - 1
  else if (event.key === 'Enter' && selectedKey.value) {
    event.preventDefault()
    emit('reveal', selectedKey.value)
    return
  }
  if (next === null) return
  event.preventDefault()
  const bar = bars[next]
  if (bar) select(bar.step.key)
}

const label = (step: Step) =>
  step.path && step.label === step.path ? relativeTo(step.path, props.task.cwd) : step.label
const summary = computed(() =>
  t('process.graph.summary', {
    count: model.steps.value.length,
    time: format.value.duration(layout.value.spanMs),
  }),
)
</script>

<template>
  <ProcessFrame
    :model="model"
    :empty="!model.steps.value.length"
    icon="graph"
    :empty-title="t('process.empty')"
    :empty-desc="t('process.emptyDesc')"
  >
    <div class="graph">
      <div class="graph__bar">
        <span class="graph__summary">{{ summary }}</span>
        <SegmentedControl
          v-model="zoom"
          :options="zoomOptions"
          :label="t('process.graph.zoom')"
          size="sm"
        />
      </div>
      <div class="graph__body">
        <ul class="graph__lanes" aria-hidden="true" :style="{ paddingTop: `${CHART.top}px` }">
          <li v-for="stage in STAGES" :key="stage" :style="{ height: `${CHART.lane}px` }">
            <StageTag :stage="stage" />
          </li>
        </ul>
        <div
          ref="scroller"
          class="graph__scroll"
          tabindex="0"
          role="group"
          :aria-label="`${t('process.graph.label')}. ${summary}`"
          @keydown="onKeydown"
        >
          <GraphChart
            :layout="layout"
            :selected-key="selectedKey"
            :label="label"
            @select="select"
          />
        </div>
      </div>
      <GraphDetail
        :step="selected"
        :label="selected ? label(selected) : ''"
        :duration-ms="selected ? durationOf(selected) : 0"
        @reveal="emit('reveal', $event)"
      />
    </div>
  </ProcessFrame>
</template>

<style scoped>
.graph {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-4) var(--space-4);
}

.graph__bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.graph__summary {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.graph__body {
  display: flex;
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
}

.graph__lanes {
  flex: none;
  padding: 0 var(--space-2);
  border-right: var(--border-width) solid var(--border-subtle);
  list-style: none;
}

.graph__lanes li {
  display: flex;
  align-items: center;
}

.graph__scroll {
  position: relative; /* contains its absolute descendants, like .pf */
  flex: 1;
  min-width: 0;
  overflow-x: auto;
  border-radius: 0 var(--radius-md) var(--radius-md) 0;
}

/* Keyboard focus only (a click never rings it). Once a bar is selected, that bar carries the focus mark instead
   of a ring around the whole graph. */
.graph__scroll:focus-visible {
  outline: none;
}

.graph__scroll:focus-visible:not(:has(.chart__rect--selected)) {
  box-shadow: inset 0 0 0 var(--focus-ring-width) var(--border-focus);
}

.graph__scroll:focus-visible :deep(.chart__rect--selected) {
  stroke-width: 3;
}

@media (width < 600px) {
  .graph {
    padding-inline: var(--space-2);
  }
}
</style>
