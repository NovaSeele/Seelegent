<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Account } from '@/api/types'
import ProgressBar from '@/components/ui/ProgressBar.vue'
import Tooltip from '@/components/ui/Tooltip.vue'
import { useFormat } from '@/composables/useFormat'
import { formatUSD } from '@/lib/usd'
import { useAccountsStore } from '@/stores/accounts'
import { useProvidersStore } from '@/stores/providers'

import AccountBalance from './AccountBalance.vue'
import { useActions } from './accountView'
import { spendView } from './spend'

/**
 * What an API key has spent, where a sign-in account shows its quota: "$12.40 / $200" with a bar (amber from 80%,
 * red at 100%), "$12.40 since 3 Nov" with "Set budget" (or, for an OpenRouter key with a limit, one click to use that
 * limit as the budget), "1.2M tokens" with "Set prices to see cost" for a service without prices, "Local · free" for
 * a local one. Claude Code's estimate carries "≈"; a tooltip says where every amount comes from. An account-wide
 * balance (DeepSeek, Moonshot) or the key's limit at OpenRouter shows beside it. `compact` is the list row's short
 * form; the card and an open row say it in full. `plain` leaves out the links (the budget dialog shows the spend
 * above its own form).
 */
const props = defineProps<{ account: Account; compact?: boolean; plain?: boolean }>()
const emit = defineEmits<{ budget: []; prices: [] }>()

const { t } = useI18n()
const fmt = useFormat()
const accounts = useAccountsStore()
const actions = useActions()

const providers = useProvidersStore()

const view = computed(() => spendView(props.account))

/** The service's count is the key's whole life, use outside Seelegent included: said plainly, every time. */
const wholeLife = computed(() => {
  if (view.value?.source !== 'service') return ''
  const a = props.account
  const service = providers.items.find((p) => p.id === a.provider)?.name ?? a.provider ?? ''
  return t('accounts.spendSource.serviceWholeLife', { service })
})
const form = computed(() => (props.compact ? 'short' : 'long'))

const text = computed(() => {
  const v = view.value
  if (!v) return ''
  if (v.kind === 'free') return t('accounts.spend.free')
  if (v.kind === 'tokens')
    return t(`accounts.spend.${form.value}.tokens`, { tokens: fmt.value.compact(v.tokens) })
  const spent = formatUSD(v.spent)
  let line: string
  if (v.budget !== undefined) {
    const left = v.budget - v.spent
    line = t(`accounts.spend.${form.value}.${left >= 0 ? 'budget' : 'over'}`, {
      spent,
      budget: formatUSD(v.budget),
      left: formatUSD(Math.abs(Math.round(left * 1000) / 1000)),
    })
  } else if (v.since && v.source !== 'service') {
    line = t(`accounts.spend.${form.value}.since`, {
      spent,
      date: fmt.value.date(v.since, { day: 'numeric', month: 'short' }),
    })
  } else line = t(`accounts.spend.${form.value}.cost`, { spent })
  return v.estimate ? `≈ ${line}` : line
})

/** Where the amount comes from: the CLI's estimate, the user's prices, or the service's own count by period. */
const tip = computed(() => {
  const v = view.value
  if (!v || v.kind !== 'cost') return ''
  if (v.source === 'cli') return t('accounts.spend.estimateTip')
  if (v.source === 'prices') return t('accounts.spendSource.prices')
  const u = props.account.serviceUsage
  if (!u) return wholeLife.value
  const parts = [
    wholeLife.value,
    t('accounts.serviceUsage.total', { amount: formatUSD(u.totalUsd) }),
    t('accounts.serviceUsage.daily', { amount: formatUSD(u.dailyUsd) }),
    t('accounts.serviceUsage.weekly', { amount: formatUSD(u.weeklyUsd) }),
    t('accounts.serviceUsage.monthly', { amount: formatUSD(u.monthlyUsd) }),
  ]
  if (u.limitUsd !== undefined) {
    const reset = u.limitReset
    parts.push(
      t('accounts.serviceUsage.limit', {
        amount: formatUSD(u.limitUsd),
        reset:
          reset === 'daily' || reset === 'weekly' || reset === 'monthly'
            ? t(`accounts.serviceUsage.reset.${reset}`)
            : t('accounts.serviceUsage.reset.never'),
      }),
    )
  }
  if (u.stale) parts.push(t('accounts.balance.stale'))
  return parts.join(' · ')
})

const barText = computed(() =>
  view.value?.ratio !== undefined ? fmt.value.percent(view.value.ratio) : undefined,
)

/** OpenRouter's limit for the key, as the budget: one click, the stop ticked as the budget dialog would. */
function useSuggested() {
  const amount = view.value?.suggested
  if (amount === undefined) return
  const a = props.account
  void actions.run(
    'suggested',
    () => accounts.update(a.id, { budgetUsd: amount, budgetStop: a.budgetStop ?? true }),
    t('accounts.done.budgetSet', { name: a.label, budget: formatUSD(amount) }),
  )
}
</script>

<template>
  <div v-if="view" class="spend" :class="{ 'spend--compact': compact }">
    <p class="spend__line">
      <Tooltip v-if="tip" :text="tip" describe>
        <span class="spend__text" tabindex="0">{{ text }}</span>
      </Tooltip>
      <span v-else class="spend__text">{{ text }}</span>
      <template v-if="!plain">
        <button
          v-if="view.needsPrices"
          type="button"
          class="spend__link"
          @click.stop="emit('prices')"
        >
          {{ t('accounts.spend.setPrices') }}
        </button>
        <button
          v-else-if="view.suggested !== undefined"
          type="button"
          class="spend__link"
          :disabled="actions.isBusy('suggested')"
          @click.stop="useSuggested"
        >
          {{ t('accounts.budget.suggested', { amount: formatUSD(view.suggested) }) }}
        </button>
        <button
          v-else-if="view.kind === 'cost' && view.budget === undefined"
          type="button"
          class="spend__link"
          @click.stop="emit('budget')"
        >
          {{ t('accounts.spend.setBudget') }}
        </button>
      </template>
    </p>
    <ProgressBar
      v-if="view.ratio !== undefined"
      :value="view.ratio"
      :tone="view.tone"
      size="sm"
      :label="t('accounts.spend.barLabel')"
      :value-text="barText"
    />
    <AccountBalance :account="account" :compact="compact" />
    <template v-if="!compact">
      <p v-if="wholeLife" class="spend__note">{{ wholeLife }}</p>
      <p v-if="view.kind === 'cost' && view.tokens > 0" class="spend__note">
        {{ t('accounts.spend.unpricedTokens', { tokens: fmt.compact(view.tokens) }) }}
        <button v-if="!plain" type="button" class="spend__link" @click.stop="emit('prices')">
          {{ t('accounts.spend.setPrices') }}
        </button>
      </p>
      <p v-if="view.budgetUnknown" class="spend__note">
        {{
          view.needsPrices
            ? t('accounts.budgetState.unknownNeedsPrices', {
                budget: formatUSD(view.budget ?? 0),
              })
            : t('accounts.spend.budgetUnknown', { budget: formatUSD(view.budget ?? 0) })
        }}
      </p>
    </template>
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

.spend__link:disabled {
  cursor: default;
  opacity: 0.6;
}

.spend__note {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}
</style>
