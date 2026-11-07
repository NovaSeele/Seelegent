<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { History } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Select from '@/components/ui/Select.vue'
import { useFormat } from '@/composables/useFormat'

import QuotaBands from './QuotaBands.vue'
import { historyAccounts, quotaSeries } from './usageModel'

/**
 * What one account had left over time (by hour for 7 days, the day's lowest for 30): one small band per quota
 * window over a shared time axis (QuotaBands), or a table of each day's lowest per window.
 */
const props = defineProps<{ history: History; minRemaining: number }>()

const { t } = useI18n()
const fmt = useFormat()
const id = useId()
const asTable = ref(false)

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
const series = computed(() => quotaSeries(props.history, accountId.value))

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
    <div v-else-if="!asTable" role="group" :aria-label="summary">
      <QuotaBands :series="series" :t0="t0" :t1="t1" :min-remaining="minRemaining" />
    </div>
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
