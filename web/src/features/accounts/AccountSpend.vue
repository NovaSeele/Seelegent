<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Account } from '@/api/types'
import ProgressBar from '@/components/ui/ProgressBar.vue'
import Tooltip from '@/components/ui/Tooltip.vue'
import { useFormat } from '@/composables/useFormat'
import { formatUSD } from '@/lib/usd'

import { spendView } from './spend'

/**
 * What an API key has spent, where a sign-in account shows its quota: "≈ $12.40 / $200" with a bar (amber from
 * 80%, red at 100%), "≈ $12.40 since 3 Nov" with "Set budget", "Cost not reported · 1.2M tokens" for a service
 * that reports no cost, "Local · free" for a local one. `compact` is the list row's short form; the card and an
 * open row say it in full. An amount with "≈" is Claude Code's own estimate, which its tooltip says.
 */
const props = defineProps<{ account: Account; compact?: boolean }>()
const emit = defineEmits<{ budget: [] }>()

const { t } = useI18n()
const fmt = useFormat()

const view = computed(() => spendView(props.account))
const form = computed(() => (props.compact ? 'short' : 'long'))

const text = computed(() => {
  const v = view.value
  if (!v) return ''
  if (v.kind === 'free') return t('accounts.spend.free')
  if (v.kind === 'tokens')
    return t(`accounts.spend.${form.value}.tokens`, { tokens: fmt.value.compact(v.tokens) })
  const spent = formatUSD(v.spent)
  if (v.budget !== undefined) {
    const left = v.budget - v.spent
    return t(`accounts.spend.${form.value}.${left >= 0 ? 'budget' : 'over'}`, {
      spent,
      budget: formatUSD(v.budget),
      left: formatUSD(Math.abs(Math.round(left * 1000) / 1000)),
    })
  }
  if (v.since)
    return t(`accounts.spend.${form.value}.since`, {
      spent,
      date: fmt.value.date(v.since, { day: 'numeric', month: 'short' }),
    })
  return t(`accounts.spend.${form.value}.cost`, { spent })
})

const barText = computed(() =>
  view.value?.ratio !== undefined ? fmt.value.percent(view.value.ratio) : undefined,
)
</script>

<template>
  <div v-if="view" class="spend" :class="{ 'spend--compact': compact }">
    <p class="spend__line">
      <Tooltip v-if="view.kind === 'cost'" :text="t('accounts.spend.estimateTip')" describe>
        <span class="spend__text" tabindex="0">{{ text }}</span>
      </Tooltip>
      <span v-else class="spend__text">{{ text }}</span>
      <button
        v-if="view.kind === 'cost' && view.budget === undefined"
        type="button"
        class="spend__link"
        @click.stop="emit('budget')"
      >
        {{ t('accounts.spend.setBudget') }}
      </button>
    </p>
    <ProgressBar
      v-if="view.ratio !== undefined"
      :value="view.ratio"
      :tone="view.tone"
      size="sm"
      :label="t('accounts.spend.barLabel')"
      :value-text="barText"
    />
    <p v-if="view.budgetUnknown && !compact" class="spend__note">
      {{ t('accounts.spend.budgetUnknown', { budget: formatUSD(view.budget ?? 0) }) }}
    </p>
  </div>
</template>

<style scoped>
.spend {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.spend__line {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0 var(--space-2);
  font-size: var(--fs-sm);
  font-variant-numeric: tabular-nums;
}

.spend--compact .spend__line {
  font-size: var(--fs-xs);
}

.spend--compact .spend__text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.spend__text:focus-visible {
  outline: 2px solid var(--border-focus);
  outline-offset: 1px;
  border-radius: var(--radius-xs);
}

.spend__link {
  padding: 0;
  border: 0;
  background: none;
  color: var(--link);
  font: inherit;
  font-size: var(--fs-xs);
  text-decoration: underline;
  cursor: pointer;
}

.spend__note {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}
</style>
