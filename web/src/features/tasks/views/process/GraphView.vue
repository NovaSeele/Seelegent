<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'

import type { TaskDetail, TaskSummary } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import { useFormat } from '@/composables/useFormat'
import { useNow } from '@/composables/useNow'
import { relativeTo } from '@/lib/paths'

import { GRAPH_DEFAULTS, layoutGraph } from './graph'
import { STAGES, type Step } from './process'
import ProcessFrame from './ProcessFrame.vue'
import StageTag from './StageTag.vue'
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

const LANE = 28
const BAR = 16
const TOP = 22

type Zoom = 'fit' | '2' | '4'
const zoom = ref<Zoom>('fit')
const zoomOptions = computed(() => [
  { value: 'fit' as const, label: t('process.graph.fit') },
  { value: '2' as const, label: '2×' },
  { value: '4' as const, label: '4×' },
])

const scroller = ref<HTMLElement | null>(null)
const available = shallowRef(0)
let observer: ResizeObserver | null = null
onMounted(() => {
  if (!scroller.value || typeof ResizeObserver === 'undefined') return
  observer = new ResizeObserver(([entry]) => (available.value = entry?.contentRect.width ?? 0))
  observer.observe(scroller.value)
})
onBeforeUnmount(() => observer?.disconnect())

const hasRunning = computed(() => model.steps.value.some((step) => step.state === 'running'))
const clock = useNow(1000)
const durationOf = (step: Step) =>
  step.state === 'running' && hasRunning.value
    ? Math.max(0, clock.value - step.start)
    : step.durationMs

const layout = computed(() => {
  const at = (pxPerSec: number) =>
    layoutGraph(model.turns.value, { ...GRAPH_DEFAULTS, pxPerSec }, durationOf)
  // Width grows linearly with px/s except for fixed parts (padding, idle breaks, minimum bar widths), so two
  // layouts give the scale that fills the width; zoom multiplies it.
  const one = at(1)
  const slope = at(2).width - one.width
  if (available.value <= 0 || slope <= 0) return at(GRAPH_DEFAULTS.pxPerSec)
  let fit = Math.min(400, Math.max(0.02, (available.value - (one.width - slope)) / slope))
  // Minimum bar widths stop mattering as the scale grows, so the estimate can overshoot: shrink until it fits.
  let fitted = at(fit)
  for (let i = 0; i < 6 && fitted.width > available.value && fit > 0.02; i++) {
    fit = Math.max(0.02, fit * (available.value / fitted.width) * 0.98)
    fitted = at(fit)
  }
  return zoom.value === 'fit' ? fitted : at(fit * Number(zoom.value))
})
const height = TOP + STAGES.length * LANE

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
        <ul class="graph__lanes" aria-hidden="true" :style="{ paddingTop: `${TOP}px` }">
          <li v-for="stage in STAGES" :key="stage" :style="{ height: `${LANE}px` }">
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
          <svg :width="layout.width" :height="height" class="graph__svg" aria-hidden="true">
            <g v-for="(_, lane) in STAGES" :key="lane">
              <line
                class="graph__lane-line"
                x1="0"
                :x2="layout.width"
                :y1="TOP + (lane + 1) * LANE - 0.5"
                :y2="TOP + (lane + 1) * LANE - 0.5"
              />
            </g>
            <g v-for="x in layout.breaks" :key="`b${x}`" class="graph__break">
              <line :x1="x" :x2="x" :y1="TOP" :y2="height" />
              <text :x="x" :y="TOP - 6" text-anchor="middle">≈</text>
              <title>{{ t('process.graph.gap') }}</title>
            </g>
            <g
              v-for="mark in layout.marks"
              :key="`m${mark.turn}.${mark.attempt}`"
              class="graph__mark"
              :class="{ 'graph__mark--handover': mark.attempt > 1 }"
            >
              <line :x1="mark.x" :x2="mark.x" :y1="4" :y2="height" />
              <text :x="mark.x + 4" y="14">
                {{
                  mark.attempt > 1
                    ? `${t('process.attempt', { n: mark.attempt })}${mark.account ? ` · ${mark.account}` : ''}`
                    : t('process.turn', { n: mark.turn })
                }}
              </text>
            </g>
            <rect
              v-for="bar in layout.bars"
              :key="bar.step.key"
              class="graph__rect"
              :class="[
                `graph__rect--${bar.step.stage}`,
                `graph__rect--${bar.step.state}`,
                { 'graph__rect--selected': bar.step.key === selectedKey },
              ]"
              :x="bar.x"
              :y="TOP + bar.lane * LANE + (LANE - BAR) / 2"
              :width="bar.w"
              :height="BAR"
              rx="3"
              @click="select(bar.step.key)"
            >
              <title>{{ bar.step.name }} {{ label(bar.step) }}</title>
            </rect>
          </svg>
        </div>
      </div>
      <section class="graph__detail" aria-live="polite">
        <template v-if="selected">
          <h3 class="sr-only">{{ t('process.graph.selected') }}</h3>
          <StageTag :stage="selected.stage" />
          <span class="graph__detail-name">{{ selected.name }}</span>
          <code class="graph__detail-label">{{ label(selected) }}</code>
          <span class="graph__detail-meta">
            {{ t(`process.state.${selected.state}`) }} · {{ format.time(selected.start) }}
            <template v-if="durationOf(selected)">
              · {{ format.duration(durationOf(selected)) }}</template
            >
          </span>
          <Button size="sm" variant="ghost" @click="emit('reveal', selected.key)">
            {{ t('process.reveal') }}
          </Button>
        </template>
        <span v-else class="graph__detail-hint">{{ t('process.graph.pick') }}</span>
      </section>
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
  flex: 1;
  min-width: 0;
  overflow-x: auto;
  border-radius: 0 var(--radius-md) var(--radius-md) 0;
}

.graph__scroll:focus-visible {
  outline: none;
  box-shadow: inset 0 0 0 var(--focus-ring-width) var(--border-focus);
}

.graph__svg {
  display: block;
}

.graph__lane-line {
  stroke: var(--border-subtle);
}

.graph__break line {
  stroke: var(--text-muted);
  stroke-dasharray: 2 3;
}

.graph__break text,
.graph__mark text {
  fill: var(--text-muted);
  font-size: var(--fs-2xs);
}

.graph__mark line {
  stroke: var(--border-strong);
  stroke-dasharray: 4 3;
}

.graph__mark--handover line {
  stroke: var(--status-waiting-fg);
}

.graph__mark--handover text {
  fill: var(--status-waiting-fg);
}

.graph__rect {
  cursor: pointer;
}

.graph__rect--explore {
  fill: var(--stage-explore);
}

.graph__rect--edit {
  fill: var(--stage-edit);
}

.graph__rect--verify {
  fill: var(--stage-verify);
}

.graph__rect--run {
  fill: var(--stage-run);
}

.graph__rect--report {
  fill: var(--stage-report);
}

.graph__rect--failed {
  stroke: var(--status-danger-fg);
  stroke-width: 2;
}

.graph__rect--running {
  opacity: 0.7;
}

.graph__rect--selected {
  stroke: var(--border-focus);
  stroke-width: 2;
}

.graph__detail {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  min-height: var(--control-md);
  font-size: var(--fs-xs);
}

.graph__detail-name {
  color: var(--text-secondary);
}

.graph__detail-label {
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  font-family: var(--font-mono);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.graph__detail-meta,
.graph__detail-hint {
  color: var(--text-muted);
}

@media (width < 600px) {
  .graph {
    padding-inline: var(--space-2);
  }
}
</style>
