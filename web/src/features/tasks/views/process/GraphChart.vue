<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import { CHART, type GraphLayout } from './graph'
import { STAGES, type Step } from './process'

/**
 * The drawing of the Graph view: lane lines, idle breaks, turn and hand-over marks and one bar per step. It is
 * hidden from screen readers; the Graph view's scroller and detail line carry the same facts.
 */
defineProps<{
  layout: GraphLayout
  selectedKey: string | null
  label: (step: Step) => string
}>()
const emit = defineEmits<{ select: [key: string] }>()

const { t } = useI18n()

const { lane: LANE, bar: BAR, top: TOP } = CHART
const height = TOP + STAGES.length * LANE
</script>

<template>
  <svg :width="layout.width" :height="height" class="chart" aria-hidden="true">
    <g v-for="(_, lane) in STAGES" :key="lane">
      <line
        class="chart__lane-line"
        x1="0"
        :x2="layout.width"
        :y1="TOP + (lane + 1) * LANE - 0.5"
        :y2="TOP + (lane + 1) * LANE - 0.5"
      />
    </g>
    <g v-for="x in layout.breaks" :key="`b${x}`" class="chart__break">
      <line :x1="x" :x2="x" :y1="TOP" :y2="height" />
      <text :x="x" :y="TOP - 6" text-anchor="middle">≈</text>
      <title>{{ t('process.graph.gap') }}</title>
    </g>
    <g
      v-for="mark in layout.marks"
      :key="`m${mark.turn}.${mark.attempt}`"
      class="chart__mark"
      :class="{ 'chart__mark--handover': mark.attempt > 1 }"
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
      class="chart__rect"
      :class="[
        `chart__rect--${bar.step.stage}`,
        `chart__rect--${bar.step.state}`,
        { 'chart__rect--selected': bar.step.key === selectedKey },
      ]"
      :x="bar.x"
      :y="TOP + bar.lane * LANE + (LANE - BAR) / 2"
      :width="bar.w"
      :height="BAR"
      rx="3"
      @click="emit('select', bar.step.key)"
    >
      <title>{{ bar.step.name }} {{ label(bar.step) }}</title>
    </rect>
  </svg>
</template>

<style scoped>
.chart {
  display: block;
}

.chart__lane-line {
  stroke: var(--border-subtle);
}

.chart__break line {
  stroke: var(--text-muted);
  stroke-dasharray: 2 3;
}

.chart__break text,
.chart__mark text {
  fill: var(--text-muted);
  font-size: var(--fs-2xs);
}

.chart__mark line {
  stroke: var(--border-strong);
  stroke-dasharray: 4 3;
}

.chart__mark--handover line {
  stroke: var(--status-waiting-fg);
}

.chart__mark--handover text {
  fill: var(--status-waiting-fg);
}

.chart__rect {
  cursor: pointer;
}

.chart__rect--explore {
  fill: var(--stage-explore);
}

.chart__rect--edit {
  fill: var(--stage-edit);
}

.chart__rect--verify {
  fill: var(--stage-verify);
}

.chart__rect--run {
  fill: var(--stage-run);
}

.chart__rect--report {
  fill: var(--stage-report);
}

.chart__rect--failed {
  stroke: var(--status-danger-fg);
  stroke-width: 2;
}

.chart__rect--running {
  opacity: 0.7;
}

.chart__rect--selected {
  stroke: var(--border-focus);
  stroke-width: 2;
}
</style>
