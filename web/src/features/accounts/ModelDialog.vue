<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage, fieldProblems } from '@/api/client'
import type { Account, KeyTestResult, TierModels } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Dialog from '@/components/ui/Dialog.vue'
import { useAccountsStore } from '@/stores/accounts'
import { useProvidersStore } from '@/stores/providers'
import { useToastStore } from '@/stores/toast'

import KeyTestNote from './KeyTestNote.vue'
import ModelPicker from './ModelPicker.vue'
import { allowList, listedModels, tierModels } from './providerForm'

/**
 * "Change model" of an API key: the model workers use is set on its service (the provider's tiers), so it
 * applies to every key of that service, which the dialog says. "List models" asks the service with the stored key.
 */
const props = defineProps<{ account: Account | undefined }>()
const open = defineModel<boolean>('open', { required: true })

const { t } = useI18n()
const accounts = useAccountsStore()
const providers = useProvidersStore()
const toast = useToastStore()

const tiers = ref<TierModels>({ fast: '', standard: '', strong: '', max: '' })
const result = ref<KeyTestResult | null>(null)
const testing = ref(false)
const busy = ref(false)
const error = ref('')

const provider = computed(() => providers.items.find((p) => p.id === props.account?.provider))
const service = computed(() => provider.value?.name ?? props.account?.provider ?? '')
const models = computed(() => {
  const p = provider.value
  const known = p ? [...tierModels(p), ...listedModels(allowList(p))] : []
  return [...new Set([...known, ...(result.value?.models ?? [])])]
})

watch(open, (value) => {
  if (!value) return
  providers.ensure()
  tiers.value = { ...(provider.value?.tiers ?? { fast: '', standard: '', strong: '', max: '' }) }
  result.value = null
  error.value = ''
})

async function listModels() {
  const a = props.account
  if (!a || testing.value) return
  testing.value = true
  try {
    result.value = await providers.test({ accountId: a.id })
    accounts.noteTest(a.id, result.value)
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    testing.value = false
  }
}

async function save() {
  const p = provider.value
  if (!p || busy.value) return
  busy.value = true
  error.value = ''
  try {
    await providers.update(p.id, { tiers: { ...tiers.value } })
    open.value = false
    toast.show({ message: t('accounts.done.modelSet', { service: p.name }), tone: 'success' })
  } catch (cause) {
    error.value = fieldProblems(cause)[0]?.message ?? errorMessage(cause)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog
    v-model:open="open"
    :title="t('accounts.model.title', { service })"
    :description="t('accounts.model.desc', { service })"
    :dismissible="!busy"
    initial-focus="select"
  >
    <form id="account-model" class="modeldlg" @submit.prevent="save">
      <ModelPicker v-model="tiers" :models="models" />
      <Button
        size="sm"
        variant="ghost"
        icon="refresh"
        class="modeldlg__list"
        :loading="testing"
        @click="listModels"
      >
        {{ t('accounts.form.listModels') }}
      </Button>
      <KeyTestNote :result="result" :testing="testing" />
      <p v-if="error" class="modeldlg__error" role="alert">{{ error }}</p>
    </form>
    <template #footer>
      <Button variant="ghost" :disabled="busy" @click="open = false">
        {{ t('common.action.cancel') }}
      </Button>
      <Button
        type="submit"
        form="account-model"
        variant="primary"
        :loading="busy"
        :disabled="!provider"
      >
        {{ t('accounts.model.save') }}
      </Button>
    </template>
  </Dialog>
</template>

<style scoped>
.modeldlg {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.modeldlg__list {
  align-self: flex-start;
}

.modeldlg__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
}
</style>
