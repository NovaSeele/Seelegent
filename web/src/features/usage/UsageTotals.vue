<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Summary } from '@/api/types'
import Icon from '@/components/ui/Icon.vue'
import { useFormat } from '@/composables/useFormat'

import { TOTAL_KEYS, totalsChange, type TotalKey } from './usageModel'

/**
 * The period's totals (tokens, cost, tasks, turns), each with its change against the period before. More usage
 * is neither good nor bad, so the change has no good/bad colour, only a direction.
 */
const props = defineProps<{ summary: Summary }>()

const { t } = useI18n()
const fmt = useFormat()

function value(key: TotalKey, n: number): string {
  if (key === 'costUsd') {
    return props.summary.totals.hasCost
      ? fmt.value.number(n, { style: 'currency', currency: 'USD' })
      : t('usage.noCost')
  }
  return key === 'tokens' ? fmt.value.compact(n) : fmt.value.number(n)
}

const tiles = computed(() =>
  TOTAL_KEYS.map((key) => {
    const delta = totalsChange(props.summary.totals, props.summary.previous, key)
    return {
      key,
      value: value(key, props.summary.totals[key]),
      exact: fmt.value.number(props.summary.totals[key]),
      previous: value(key, props.summary.previous[key]),
      delta,
      direction: delta === null || delta === 0 ? 'same' : delta > 0 ? 'up' : 'down',
    }
  }),
)
</script>

<template>
  <dl class="totals">
    <div v-for="tile in tiles" :key="tile.key" class="total">
      <dt class="total__label">{{ t(`usage.totals.${tile.key}`) }}</dt>
      <dd class="total__value" :title="tile.key === 'tokens' ? tile.exact : undefined">
        {{ tile.value }}
      </dd>
      <dd class="total__delta">
        <Icon v-if="tile.direction === 'up'" name="chevronUp" />
        <Icon v-else-if="tile.direction === 'down'" name="chevronDown" />
        <span>
          {{
            tile.delta === null
              ? t('usage.delta.new', { previous: tile.previous })
              : t(`usage.delta.${tile.direction}`, {
                  pct: fmt.percent(Math.abs(tile.delta), 0),
                  previous: tile.previous,
                })
          }}
        </span>
      </dd>
    </div>
  </dl>
</template>

<style scoped>
.totals {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
  gap: var(--space-2);
}

.total {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding: var(--space-3) var(--space-4);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
}

.total__label {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.total__value {
  font-size: var(--fs-xl);
  font-weight: var(--fw-semibold);
  font-variant-numeric: tabular-nums;
}

.total__delta {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  color: var(--text-secondary);
  font-size: var(--fs-2xs);
}
</style>
