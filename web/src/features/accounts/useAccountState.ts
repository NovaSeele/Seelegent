import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Account } from '@/api/types'
import { useFormat } from '@/composables/useFormat'
import { useAccountsStore } from '@/stores/accounts'

import { PILL_TONE, pillState, STATUS_TONE } from './accountView'

/**
 * The words for an account's state, shared by its card and the card's chips: out of quota and resting (with
 * when they end), the twin's name, and the pill (status word, or out of quota / resting in its place).
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
  const restingLabel = computed(() =>
    props.account.restingUntil
      ? t('accounts.restingUntil', {
          time: fmt.value.relative(props.account.restingUntil, props.now),
        })
      : t('accounts.resting'),
  )
  const pill = computed(() => {
    const state = pillState(props.account, props.now)
    if (state === 'exhausted')
      return { state, tone: PILL_TONE.exhausted, label: exhaustedLabel.value }
    if (state === 'resting') return { state, tone: PILL_TONE.resting, label: restingLabel.value }
    return {
      state,
      tone: STATUS_TONE[props.account.status],
      label: t(`accounts.status.${props.account.status}`, { name: twinLabel.value }),
    }
  })

  return { twinLabel, exhaustedLabel, restingLabel, pill }
}
