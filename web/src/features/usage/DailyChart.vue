<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import type { DayTotal } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import { useFormat } from '@/composables/useFormat'

import { niceMax } from './usageModel'

/**
 * Tokens per day as bars. The SVG holds only shapes (it stretches to the width); the axis labels are HTML, so
 * text never distorts. Screen readers get a one-line summary, and "Show as table" swaps the chart for the numbers.
 */
const props = defineProps<{ daily: readonly DayTotal[]; hasCost: boolean }>()

const { t } = useI18n()
const fmt = useFormat()
const id = useId()
const asTable = ref(false)

const W = 600
const H = 160
const max = computed(() => niceMax(Math.max(0, ...props.daily.map((d) => d.tokens))))

const day = (date: string) => {
  const [y, m, d] = date.split('-').map(Number)
  return new Date(y ?? 1970, (m ?? 1) - 1, d ?? 1)
}
const short = (date: string) => fmt.value.date(day(date), { month: 'short', day: 'numeric' })

const bars = computed(() => {
  const slot = W / Math.max(1, props.daily.length)
  const gap = Math.min(6, slot * 0.25)
  return props.daily.map((d, i) => {
    const h = (d.tokens / max.value) * H
    return { ...d, x: i * slot + gap / 2, w: slot - gap, y: H - h, h }
  })
})

const ticks = computed(() => {
  const list = props.daily
  if (!list.length) return []
  const picks = list.length <= 7 ? list : [list[0], list[Math.floor(list.length / 2)], list.at(-1)]
  return picks.flatMap((d) => (d ? [short(d.date)] : []))
})

const summary = computed(() => {
  const list = props.daily
  if (!list.length) return t('usage.daily.empty')
  const top = list.reduce((a, b) => (b.tokens > a.tokens ? b : a))
  return t('usage.daily.summary', {
    from: short(list[0]?.date ?? ''),
    to: short(list.at(-1)?.date ?? ''),
    top: fmt.value.compact(top.tokens),
    day: short(top.date),
  })
})
</script>

<template>
  <figure class="daily" :aria-labelledby="`${id}-title`">
    <figcaption class="daily__head">
      <h2 :id="`${id}-title`" class="daily__title">{{ t('usage.daily.title') }}</h2>
      <Button size="sm" variant="ghost" :aria-pressed="asTable" @click="asTable = !asTable">
        {{ asTable ? t('usage.showChart') : t('usage.showTable') }}
      </Button>
    </figcaption>

    <div v-if="!asTable" class="daily__chart">
      <span class="daily__max">{{ fmt.compact(max) }}</span>
      <svg
        class="daily__svg"
        :viewBox="`0 0 ${W} ${H}`"
        preserveAspectRatio="none"
        role="img"
        :aria-label="summary"
      >
        <line x1="0" :y1="H / 2" :x2="W" :y2="H / 2" class="daily__grid" />
        <line x1="0" :y1="H" :x2="W" :y2="H" class="daily__axis" />
        <rect
          v-for="bar in bars"
          :key="bar.date"
          class="daily__bar"
          :x="bar.x"
          :y="bar.y"
          :width="bar.w"
          :height="bar.h"
          rx="2"
        >
          <title>{{ `${short(bar.date)}: ${fmt.number(bar.tokens)}` }}</title>
        </rect>
      </svg>
      <div class="daily__ticks" aria-hidden="true">
        <span v-for="tick in ticks" :key="tick">{{ tick }}</span>
      </div>
    </div>

    <table v-else class="daily__table">
      <thead>
        <tr>
          <th scope="col">{{ t('usage.col.date') }}</th>
          <th scope="col">{{ t('usage.col.tokens') }}</th>
          <th v-if="hasCost" scope="col">{{ t('usage.col.cost') }}</th>
          <th scope="col">{{ t('usage.col.tasks') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="d in daily" :key="d.date">
          <th scope="row">{{ fmt.date(day(d.date)) }}</th>
          <td>{{ fmt.number(d.tokens) }}</td>
          <td v-if="hasCost">
            {{ fmt.number(d.costUsd, { style: 'currency', currency: 'USD' }) }}
          </td>
          <td>{{ fmt.number(d.tasks) }}</td>
        </tr>
      </tbody>
    </table>
  </figure>
</template>

<style scoped>
.daily {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  margin: 0;
  padding: var(--space-4);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
}

.daily__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.daily__title {
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
}

.daily__chart {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.daily__max,
.daily__ticks {
  color: var(--text-muted);
  font-size: var(--fs-2xs);
  font-variant-numeric: tabular-nums;
}

.daily__svg {
  width: 100%;
  height: 160px;
  overflow: visible;
}

.daily__grid {
  stroke: var(--border-subtle);
  stroke-dasharray: 4 4;
  vector-effect: non-scaling-stroke;
}

.daily__axis {
  stroke: var(--border-strong);
  vector-effect: non-scaling-stroke;
}

.daily__bar {
  fill: var(--accent);
}

.daily__bar:hover {
  fill: var(--accent-hover);
}

.daily__ticks {
  display: flex;
  justify-content: space-between;
}

.daily__table {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--fs-sm);
  font-variant-numeric: tabular-nums;
}

.daily__table th,
.daily__table td {
  padding: var(--space-1) var(--space-2);
  border-bottom: var(--border-width) solid var(--border-subtle);
  text-align: right;
}

.daily__table th:first-child {
  text-align: left;
}

.daily__table thead th {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}
</style>
