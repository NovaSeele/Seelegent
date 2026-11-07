<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { History } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Select from '@/components/ui/Select.vue'
import { useFormat } from '@/composables/useFormat'

import { historyAccounts, linePath, quotaSeries } from './usageModel'

/**
 * What one account had left over time, one line per quota window (by hour for 7 days, the day's lowest for 30),
 * with the "rest below" threshold (minRemaining) as a dashed line. Lines differ in dash pattern as well as colour,
 * and the table view lists each day's lowest per window.
 */
const props = defineProps<{ history: History; minRemaining: number }>()

const { t } = useI18n()
const fmt = useFormat()
const id = useId()
const asTable = ref(false)

const W = 600
const H = 160
const STYLES = [
  { color: 'var(--accent)', dash: '' },
  { color: 'var(--text-primary)', dash: '6 4' },
  { color: 'var(--text-secondary)', dash: '2 3' },
  { color: 'var(--text-muted)', dash: '10 3 2 3' },
]

const accounts = computed(() => historyAccounts(props.history))
const accountId = ref('')
watch(
  accounts,
  (list) => {
    if (!list.some((a) => a.id === accountId.value)) accountId.value = list[0]?.id ?? ''
  },
  { immediate: true },
)
const options = computed(() => accounts.value.map((a) => ({ value: a.id, label: a.label })))

const t0 = computed(() => Date.parse(props.history.since))
const t1 = computed(() => Date.parse(props.history.until))
const series = computed(() =>
  quotaSeries(props.history, accountId.value).map((s, i) => ({
    ...s,
    style: STYLES[i % STYLES.length] ?? { color: 'var(--accent)', dash: '' },
    d: linePath(s.points, t0.value, t1.value, W, H),
  })),
)

const ticks = computed(() => [
  fmt.value.date(t0.value, { month: 'short', day: 'numeric' }),
  fmt.value.date(t1.value, { month: 'short', day: 'numeric' }),
])

/** Each local day's lowest remaining per window. */
const rows = computed(() => {
  const days = new Map<string, Map<string, number>>()
  for (const s of series.value) {
    for (const p of s.points) {
      const day = fmt.value.date(p.t)
      const cells = days.get(day) ?? new Map<string, number>()
      cells.set(s.key, Math.min(cells.get(s.key) ?? 1, p.v))
      days.set(day, cells)
    }
  }
  return [...days.entries()].map(([day, cells]) => ({ day, cells }))
})

const summary = computed(() =>
  series.value.length
    ? t('usage.quota.summary', {
        lines: series.value
          .map((s) => t('usage.quota.low', { key: s.key, pct: fmt.value.percent(s.low, 0) }))
          .join('; '),
      })
    : t('usage.quota.empty'),
)
</script>

<template>
  <figure class="qh" :aria-labelledby="`${id}-title`">
    <figcaption class="qh__head">
      <h2 :id="`${id}-title`" class="qh__title">{{ t('usage.quota.title') }}</h2>
      <div class="qh__tools">
        <Select
          v-if="options.length"
          v-model="accountId"
          :label="t('usage.quota.account')"
          hide-label
          size="sm"
          :options="options"
        />
        <Button size="sm" variant="ghost" :aria-pressed="asTable" @click="asTable = !asTable">
          {{ asTable ? t('usage.showChart') : t('usage.showTable') }}
        </Button>
      </div>
    </figcaption>

    <p v-if="!series.length" class="qh__empty">{{ t('usage.quota.empty') }}</p>
    <template v-else-if="!asTable">
      <div class="qh__chart">
        <div class="qh__axis" aria-hidden="true">
          <span>100%</span>
          <span>50%</span>
          <span>0%</span>
        </div>
        <svg
          class="qh__svg"
          :viewBox="`0 0 ${W} ${H}`"
          preserveAspectRatio="none"
          role="img"
          :aria-label="summary"
        >
          <line x1="0" :y1="H / 2" :x2="W" :y2="H / 2" class="qh__grid" />
          <line x1="0" :y1="H" :x2="W" :y2="H" class="qh__grid" />
          <line
            v-if="minRemaining > 0"
            x1="0"
            :y1="(1 - minRemaining) * H"
            :x2="W"
            :y2="(1 - minRemaining) * H"
            class="qh__min"
          />
          <path
            v-for="s in series"
            :key="s.key"
            :d="s.d"
            class="qh__line"
            :stroke="s.style.color"
            :stroke-dasharray="s.style.dash || undefined"
          />
        </svg>
      </div>
      <div class="qh__ticks" aria-hidden="true">
        <span v-for="tick in ticks" :key="tick">{{ tick }}</span>
      </div>
      <ul class="qh__legend">
        <li v-for="s in series" :key="s.key">
          <svg width="28" height="8" aria-hidden="true">
            <line
              x1="0"
              y1="4"
              x2="28"
              y2="4"
              :stroke="s.style.color"
              stroke-width="2"
              :stroke-dasharray="s.style.dash || undefined"
            />
          </svg>
          {{ t('usage.quota.low', { key: s.key, pct: fmt.percent(s.low, 0) }) }}
        </li>
        <li v-if="minRemaining > 0">
          <svg width="28" height="8" aria-hidden="true">
            <line x1="0" y1="4" x2="28" y2="4" class="qh__min" />
          </svg>
          {{ t('usage.quota.threshold', { pct: fmt.percent(minRemaining, 0) }) }}
        </li>
      </ul>
    </template>
    <div v-else class="qh__scroll">
      <table class="qh__table">
        <thead>
          <tr>
            <th scope="col">{{ t('usage.col.date') }}</th>
            <th v-for="s in series" :key="s.key" scope="col">{{ s.key }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in rows" :key="row.day">
            <th scope="row">{{ row.day }}</th>
            <td v-for="s in series" :key="s.key">
              {{ row.cells.has(s.key) ? fmt.percent(row.cells.get(s.key) ?? 0, 0) : '—' }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </figure>
</template>

<style scoped>
.qh {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: 0;
  padding: var(--space-4);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
}

.qh__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.qh__title {
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
}

.qh__tools {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.qh__empty {
  color: var(--text-muted);
  font-size: var(--fs-sm);
}

.qh__chart {
  display: flex;
  gap: var(--space-2);
}

.qh__axis {
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  height: 160px;
  margin: -6px 0;
}

.qh__axis,
.qh__ticks {
  color: var(--text-muted);
  font-size: var(--fs-2xs);
  font-variant-numeric: tabular-nums;
}

.qh__svg {
  flex: 1;
  min-width: 0;
  height: 160px;
  overflow: visible;
}

.qh__grid {
  stroke: var(--border-subtle);
  vector-effect: non-scaling-stroke;
}

.qh__min {
  stroke: var(--attention-fg);
  stroke-dasharray: 3 3;
  stroke-width: 1.5;
  vector-effect: non-scaling-stroke;
}

.qh__line {
  fill: none;
  stroke-width: 2;
  stroke-linejoin: round;
  vector-effect: non-scaling-stroke;
}

.qh__ticks {
  display: flex;
  justify-content: space-between;
  padding-left: calc(4ch + var(--space-2));
}

.qh__legend {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-4);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  margin: 0;
  padding: 0;
  list-style: none;
}

.qh__legend li {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.qh__scroll {
  max-height: 360px;
  overflow: auto;
}

.qh__table {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--fs-sm);
  font-variant-numeric: tabular-nums;
}

.qh__table th,
.qh__table td {
  padding: var(--space-1) var(--space-2);
  border-bottom: var(--border-width) solid var(--border-subtle);
  text-align: right;
  white-space: nowrap;
}

.qh__table th:first-child {
  text-align: left;
}

.qh__table thead th {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}
</style>
