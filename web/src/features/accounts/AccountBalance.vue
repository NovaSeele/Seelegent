<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Account } from '@/api/types'
import Tooltip from '@/components/ui/Tooltip.vue'
import { useFormat } from '@/composables/useFormat'
import { formatMoney } from '@/lib/usd'
import { useProvidersStore } from '@/stores/providers'

/**
 * What the service says is left, beside the spend: "Balance ¥187.60 (account-wide, read 2 min ago)". A DeepSeek or
 * Moonshot balance belongs to the whole service account (every key and app on it), so it is never the key's spend;
 * an OpenRouter key's balance is what its own limit leaves. The currency is the one the service sent. A reading that
 * may be out of date, or one that cannot pay, says so; a failed reading shows its reason (the service's English
 * message is the tooltip).
 */
const props = defineProps<{ account: Account; compact?: boolean }>()

const { t, locale } = useI18n()
const fmt = useFormat()
const providers = useProvidersStore()

const balance = computed(() => props.account.balance)
const service = computed(
  () =>
    providers.items.find((p) => p.id === props.account.provider)?.name ??
    props.account.provider ??
    '',
)
const money = (amount: number, currency: string) => formatMoney(amount, currency, locale.value)

const amount = computed(() => {
  const b = balance.value
  return b ? t('accounts.balance.title', { amount: money(b.amount, b.currency) }) : ''
})

/** "(account-wide, read 2 min ago)": the long form only; the short one keeps it in the tooltip. */
const detail = computed(() => {
  const b = balance.value
  if (!b) return ''
  return [
    b.accountWide ? t('accounts.balance.shared') : '',
    t('accounts.balance.readAt', { time: fmt.value.relative(b.readAt) }),
  ]
    .filter(Boolean)
    .join(', ')
})

const tip = computed(() => {
  const b = balance.value
  if (!b) return ''
  return [
    b.accountWide ? t('accounts.balance.accountWide', { provider: service.value }) : '',
    b.paid !== undefined ? t('accounts.balance.paid', { amount: money(b.paid, b.currency) }) : '',
    b.granted !== undefined
      ? t('accounts.balance.granted', { amount: money(b.granted, b.currency) })
      : '',
    ...(b.other ?? []).map((m) => money(m.amount, m.currency)),
    props.compact ? detail.value : '',
    b.stale ? t('accounts.balance.stale') : '',
  ]
    .filter(Boolean)
    .join(' · ')
})

const error = computed(() => props.account.serviceError)
</script>

<template>
  <p v-if="balance" class="bal" :class="{ 'bal--off': !balance.available || balance.stale }">
    <Tooltip :text="tip" describe>
      <span class="bal__amount" tabindex="0">{{ amount }}</span>
    </Tooltip>
    <span v-if="!compact" class="bal__detail">({{ detail }})</span>
    <span v-if="!balance.available" class="bal__flag">{{ t('accounts.balance.unavailable') }}</span>
    <span v-else-if="balance.stale" class="bal__flag">{{ t('accounts.balance.stale') }}</span>
  </p>
  <p v-if="error && !compact" class="bal bal--error" :title="error.message">
    {{ t(`accounts.serviceError.${error.reason}`) }}
  </p>
</template>

<style scoped>
.bal {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0 var(--space-2);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-variant-numeric: tabular-nums;
}

.bal__amount {
  color: var(--text-primary);
}

.bal__amount:focus-visible {
  outline: 2px solid var(--border-focus);
  outline-offset: 1px;
  border-radius: var(--radius-xs);
}

.bal__flag {
  color: var(--attention-fg);
  font-weight: var(--fw-medium);
}

.bal--error {
  color: var(--attention-fg);
}
</style>
