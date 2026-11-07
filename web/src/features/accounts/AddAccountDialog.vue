<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage, fieldProblems } from '@/api/client'
import type { Account } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import ChoiceGroup from '@/components/ui/ChoiceGroup.vue'
import Dialog from '@/components/ui/Dialog.vue'
import NumberField from '@/components/ui/NumberField.vue'
import Select from '@/components/ui/Select.vue'
import TextField from '@/components/ui/TextField.vue'
import { useAccountsStore } from '@/stores/accounts'
import { useProvidersStore } from '@/stores/providers'
import { useToastStore } from '@/stores/toast'

import { BACKENDS, kindKey } from './accountView'

/**
 * Adding an account in two steps: what it is (backend and kind), then its details (name, priority; for an API
 * key the provider and, optionally, the key). An isolated account starts its sign-in right away. The key field is
 * cleared whenever the dialog closes, and a key the daemon refuses removes the new account again (store).
 */
const open = defineModel<boolean>('open', { required: true })

const { t } = useI18n()
const accounts = useAccountsStore()
const providers = useProvidersStore()
const toast = useToastStore()

const step = ref<1 | 2>(1)
const backend = ref<Account['backend']>('agy')
const kind = ref<Account['kind']>('isolated')
const label = ref('')
const priority = ref(0)
const provider = ref('')
const key = ref('')
const busy = ref(false)
const errors = ref<Record<string, string>>({})
const failure = ref('')
const formEl = ref<HTMLFormElement | null>(null)

const hasSystem = computed(() => accounts.accounts.some((a) => a.kind === 'system'))

const backendOptions = computed(() =>
  BACKENDS.map((value) => ({
    value,
    label: t(`accounts.backend.${value}`),
    description: t(`accounts.add.backendDesc.${value}`),
  })),
)

const kindOptions = computed(() => {
  const kinds: Account['kind'][] =
    backend.value === 'agy' ? ['isolated', 'system'] : ['isolated', 'api-key']
  return kinds.map((value) => ({
    value,
    label: t(`accounts.kind.${kindKey(value)}`),
    description: t(`accounts.add.kindDesc.${kindKey(value)}`),
    disabled: value === 'system' && hasSystem.value,
  }))
})

const providerOptions = computed(() =>
  providers.items
    .filter((p) => p.backend === backend.value && p.enabled)
    .map((p) => ({ value: p.id, label: p.name })),
)

watch(open, (value) => {
  if (value) {
    providers.ensure()
    return
  }
  step.value = 1
  key.value = ''
  label.value = ''
  priority.value = 0
  errors.value = {}
  failure.value = ''
})

watch(backend, () => {
  if (!kindOptions.value.some((o) => o.value === kind.value && !o.disabled)) kind.value = 'isolated'
})

watch(providerOptions, (options) => {
  if (!options.some((o) => o.value === provider.value)) provider.value = options[0]?.value ?? ''
})

/** Step 2 starts at the name field (the submit button keeps focus otherwise, and Enter would add at once). */
async function toDetails() {
  step.value = 2
  if (!label.value) {
    const count = accounts.accounts.filter((a) => a.backend === backend.value).length
    label.value = t('accounts.add.defaultLabel', {
      backend: t(`accounts.backend.${backend.value}`),
      n: count + 1,
    })
  }
  await nextTick()
  const first = formEl.value?.querySelector<HTMLInputElement>('input')
  first?.focus()
  first?.select()
}

async function submit() {
  if (busy.value) return
  busy.value = true
  errors.value = {}
  failure.value = ''
  try {
    const apiKey = kind.value === 'api-key' && key.value.trim() ? key.value.trim() : undefined
    const created = await accounts.create(
      {
        backend: backend.value,
        kind: kind.value,
        label: label.value.trim(),
        priority: Math.round(priority.value),
        ...(kind.value === 'api-key' ? { provider: provider.value } : {}),
      },
      apiKey,
    )
    open.value = false
    toast.show({ message: t('accounts.done.added', { name: created.label }), tone: 'success' })
    if (created.kind === 'isolated') await accounts.login(created.id).catch(() => undefined)
  } catch (error) {
    const problems = fieldProblems(error)
    errors.value = Object.fromEntries(problems.map((p) => [p.field, p.message]))
    if (!problems.length) failure.value = errorMessage(error)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog
    v-model:open="open"
    :title="t('accounts.add.title')"
    :description="t(step === 1 ? 'accounts.add.step1' : 'accounts.add.step2')"
    :dismissible="!busy"
  >
    <form
      id="add-account"
      ref="formEl"
      class="add"
      @submit.prevent="step === 1 ? toDetails() : submit()"
    >
      <template v-if="step === 1">
        <ChoiceGroup
          v-model="backend"
          :legend="t('accounts.add.backend')"
          :options="backendOptions"
        />
        <ChoiceGroup v-model="kind" :legend="t('accounts.add.kind')" :options="kindOptions" />
      </template>
      <template v-else>
        <TextField v-model="label" :label="t('accounts.add.label')" :error="errors.label" />
        <NumberField
          :model-value="priority"
          :label="t('accounts.priority')"
          :hint="t('accounts.add.priorityHint')"
          :error="errors.priority"
          @commit="priority = $event"
        />
        <template v-if="kind === 'api-key'">
          <Select
            v-if="providerOptions.length"
            v-model="provider"
            :label="t('accounts.add.provider')"
            :options="providerOptions"
          />
          <p v-else class="add__note">{{ t('accounts.add.noProvider') }}</p>
          <p v-if="errors.provider" class="add__error">{{ errors.provider }}</p>
          <TextField
            v-model="key"
            type="password"
            autocomplete="new-password"
            :label="t('accounts.key.label')"
            :hint="t('accounts.add.keyHint')"
            :error="errors.key"
          />
        </template>
      </template>
      <p v-if="failure" class="add__error" role="alert">{{ failure }}</p>
    </form>
    <template #footer>
      <Button v-if="step === 2" variant="ghost" :disabled="busy" @click="step = 1">
        {{ t('common.action.back') }}
      </Button>
      <Button
        type="submit"
        form="add-account"
        variant="primary"
        :loading="busy"
        :disabled="step === 2 && kind === 'api-key' && !provider"
      >
        {{ step === 1 ? t('accounts.add.next') : t('accounts.add.submit') }}
      </Button>
    </template>
  </Dialog>
</template>

<style scoped>
.add {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.add__note {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.add__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
}
</style>
