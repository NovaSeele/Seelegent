import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Account } from '@/api/types'
import { useFormat } from '@/composables/useFormat'
import { formatUSD } from '@/lib/usd'
import { useAccountsStore } from '@/stores/accounts'

import { PILL_TONE, pillState, STATUS_TONE } from './accountView'

/**
 * The words for an account's state, shared by its card and the card's chips: out of quota and resting (with
 * when they end; a hold until the user acts says why, never a date), the twin's name, and the pill (status word,
 * or held / out of quota / resting in its place) with its tooltip (a hold adds what to do).
 */
export function useAccountState(props: { readonly account: Account; readonly now: number }) {
  const { t } = useI18n()
  const fmt = useFormat()
  const accounts = useAccountsStore()

  const twinLabel = computed(() => {
    const twin = props.account.twinOf
    return twin ? (accounts.get(twin)?.label ?? twin) : ''
  })
  const exhaustedLabel = computed(() =>
    t('accounts.exhausted', {
      time: fmt.value.relative(props.account.quota.exhaustedUntil ?? '', props.now),
    }),
  )
  const holdLabel = computed(() => {
    const a = props.account
    if (a.restReason === 'credits') return t('accounts.restReason.credits')
    if (a.restReason === 'budget_reached')
      return t('accounts.restReason.budget_reached', {
        spent: formatUSD(a.spentUsd ?? 0),
        budget: formatUSD(a.budgetUsd ?? 0),
      })
    return ''
  })
  /** What to do about a hold ("" when there is nothing more to say than the label). */
  const holdHint = computed(() =>
    props.account.restReason === 'credits' ? t('accounts.restReason.creditsHint') : '',
  )
  const restingLabel = computed(() => {
    if (holdLabel.value) return holdLabel.value
    return props.account.restingUntil
      ? t('accounts.restingUntil', {
          time: fmt.value.relative(props.account.restingUntil, props.now),
        })
      : t('accounts.resting')
  })
  const pill = computed(() => {
    const state = pillState(props.account, props.now)
    if (state === 'held') {
      const label = holdLabel.value
      return {
        state,
        tone: PILL_TONE.held,
        label,
        title: holdHint.value ? `${label}. ${holdHint.value}` : label,
      }
    }
    if (state === 'exhausted') {
      const label = exhaustedLabel.value
      return { state, tone: PILL_TONE.exhausted, label, title: label }
    }
    if (state === 'resting') {
      const label = restingLabel.value
      return { state, tone: PILL_TONE.resting, label, title: label }
    }
    const label = t(`accounts.status.${props.account.status}`, { name: twinLabel.value })
    return { state, tone: STATUS_TONE[props.account.status], label, title: label }
  })

  return { twinLabel, exhaustedLabel, restingLabel, holdHint, pill }
}
