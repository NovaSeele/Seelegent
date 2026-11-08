<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Account } from '@/api/types'
import { useProvidersStore } from '@/stores/providers'

import AccountSpend from './AccountSpend.vue'
import { tierModels } from './providerForm'
import { useAccountState } from './useAccountState'

/**
 * An API key's facts, where a sign-in account shows its quota: the last key test and when it ran (the status pill
 * already says "Not tested"), the model workers use ("chosen per task" when the service has none), the spend, and
 * the routing rule: an API-key account only takes tasks that ask for its service (pool/select.go), said plainly.
 */
const props = defineProps<{ account: Account; now: number }>()
const emit = defineEmits<{ budget: [] }>()

const { t } = useI18n()
const providers = useProvidersStore()
const { testTitle } = useAccountState(props)

const provider = computed(() => providers.items.find((p) => p.id === props.account.provider))
const service = computed(() => provider.value?.name ?? props.account.provider ?? '')

const model = computed(() => {
  const p = provider.value
  if (props.account.modelPerTask || (p && p.modelPerTask)) return t('accounts.modelPerTask')
  const models = p ? tierModels(p) : []
  if (!models.length) return ''
  return models.length === 1
    ? t('accounts.facts.model', { model: models[0] })
    : t('accounts.facts.models', { models: models.join(', ') })
})
</script>

<template>
  <div class="keyfacts">
    <p v-if="account.testedAt" class="keyfacts__line">{{ testTitle }}</p>
    <p v-if="model" class="keyfacts__line">{{ model }}</p>
    <AccountSpend :account="account" @budget="emit('budget')" />
    <p v-if="account.provider" class="keyfacts__routing">
      {{ t('accounts.routing', { service, id: account.provider }) }}
    </p>
  </div>
</template>

<style scoped>
.keyfacts {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  max-width: 560px;
}

.keyfacts__line {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.keyfacts__routing {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}
</style>
