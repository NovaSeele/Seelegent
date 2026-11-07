<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { useFormat } from '@/composables/useFormat'

import { dayStarts, stepPath, type QuotaSeries } from './usageModel'

/**
 * Quota windows of one account as small bands, one per window, over a shared time axis with a gridline at each
 * local midnight. Each band is a step line (a value holds until the next sample, a reset is a vertical step) on
 * a 0–100 % scale, with the "rest below" threshold (minRemaining) dashed.
 */
const props = defineProps<{
  series: readonly QuotaSeries[]
  t0: number
  t1: number
  minRemaining: number
}>()

const { t } = useI18n()
const fmt = useFormat()

const W = 600
const H = 40
/** At most this many day labels; with more days, every n-th is labelled (the gridlines stay). */
const MAX_LABELS = 8

const span = computed(() => Math.max(1, props.t1 - props.t0))
const days = computed(() => dayStarts(props.t0, props.t1))
const gridX = computed(() => days.value.map((day) => ((day - props.t0) / span.value) * W))
const labels = computed(() => {
  const every = Math.ceil(days.value.length / MAX_LABELS)
  return days.value
    .filter((_, i) => i % every === 0)
    .map((day) => ({
      day,
      left: ((day - props.t0) / span.value) * 100,
      text: fmt.value.date(day, { month: 'short', day: 'numeric' }),
    }))
    .filter((label) => label.left <= 94)
})
const bands = computed(() =>
  props.series.map((s) => ({
    ...s,
    d: stepPath(s.points, props.t0, props.t1, W, H),
    title: t('usage.quota.low', { key: s.key, pct: fmt.value.percent(s.low, 0) }),
  })),
)
const thresholdY = computed(() => (1 - props.minRemaining) * H)
</script>

<template>
  <div class="qb">
    <div v-for="band in bands" :key="band.key" class="qb__band">
      <p class="qb__name">{{ band.title }}</p>
      <div class="qb__row">
        <div class="qb__axis" aria-hidden="true">
          <span>100%</span>
          <span>0%</span>
        </div>
        <svg
          class="qb__svg"
          :viewBox="`0 0 ${W} ${H}`"
          preserveAspectRatio="none"
          role="img"
          :aria-label="band.title"
        >
          <line v-for="x in gridX" :key="x" :x1="x" :x2="x" y1="0" :y2="H" class="qb__grid" />
          <line x1="0" :y1="H" :x2="W" :y2="H" class="qb__base" />
          <line
            v-if="minRemaining > 0"
            x1="0"
            :y1="thresholdY"
            :x2="W"
            :y2="thresholdY"
            class="qb__min"
          />
          <path :d="band.d" class="qb__line" />
        </svg>
      </div>
    </div>
    <div class="qb__ticks" aria-hidden="true">
      <span v-for="label in labels" :key="label.day" :style="{ left: `${label.left}%` }">
        {{ label.text }}
      </span>
    </div>
    <p v-if="minRemaining > 0" class="qb__note">
      <svg width="24" height="8" aria-hidden="true">
        <line x1="0" y1="4" x2="24" y2="4" class="qb__min" />
      </svg>
      {{ t('usage.quota.threshold', { pct: fmt.percent(minRemaining, 0) }) }}
    </p>
  </div>
</template>

<style scoped>
.qb {
  --qb-axis: 4ch;

  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.qb__band {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.qb__name {
  margin-left: calc(var(--qb-axis) + var(--space-2));
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.qb__row {
  display: flex;
  gap: var(--space-2);
}

.qb__axis {
  display: flex;
  flex: none;
  flex-direction: column;
  justify-content: space-between;
  width: var(--qb-axis);
  height: 40px;
  margin: -5px 0;
  text-align: right;
}

.qb__axis,
.qb__ticks {
  color: var(--text-muted);
  font-size: var(--fs-2xs);
  font-variant-numeric: tabular-nums;
}

.qb__svg {
  flex: 1;
  min-width: 0;
  height: 40px;
  overflow: visible;
}

.qb__grid,
.qb__base {
  stroke: var(--border-strong);
  vector-effect: non-scaling-stroke;
}

.qb__grid {
  stroke-dasharray: 2 3;
}

.qb__min {
  stroke: var(--attention-fg);
  stroke-dasharray: 3 3;
  stroke-width: 1.5;
  vector-effect: non-scaling-stroke;
}

.qb__line {
  fill: none;
  stroke: var(--accent);
  stroke-width: 2;
  stroke-linejoin: round;
  vector-effect: non-scaling-stroke;
}

.qb__ticks {
  position: relative;
  height: 1.4em;
  margin-left: calc(var(--qb-axis) + var(--space-2));
}

.qb__ticks span {
  position: absolute;
  top: 0;
  padding-left: var(--space-1);
  white-space: nowrap;
}

.qb__note {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}
</style>
