<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage, fieldProblems } from '@/api/client'
import type { Provider, ProviderPreset } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Dialog from '@/components/ui/Dialog.vue'
import Select from '@/components/ui/Select.vue'
import Switch from '@/components/ui/Switch.vue'
import TextField from '@/components/ui/TextField.vue'
import { useProvidersStore } from '@/stores/providers'
import { useToastStore } from '@/stores/toast'

import { parseModels, providerChanges, providerInput } from './providerForm'

/**
 * Adds a provider (from a preset, or blank) or edits one. The id and backend are fixed once the provider exists;
 * models are a comma-separated list. Field errors from the daemon show next to their field.
 */
const props = defineProps<{ provider: Provider | null }>()
const open = defineModel<boolean>('open', { required: true })

const { t } = useI18n()
const providers = useProvidersStore()
const toast = useToastStore()

const preset = ref('')
const form = ref<ProviderPreset>(blank())
const models = ref('')
const busy = ref(false)
const errors = ref<Record<string, string>>({})
const failure = ref('')

function blank(): ProviderPreset {
  return {
    id: '',
    name: '',
    backend: 'claude',
    baseUrl: '',
    enabled: true,
    models: [],
    official: false,
  }
}

const editing = computed(() => props.provider !== null)

const presetOptions = computed(() => [
  { value: '', label: t('accounts.providers.blank') },
  ...providers.presets
    .filter((p) => !providers.items.some((item) => item.id === p.id))
    .map((p) => ({
      value: p.id,
      label: `${p.name} (${t(`accounts.backend.${p.backend}`)})`,
    })),
])

const backendOptions = computed(() =>
  (['claude', 'codex'] as const).map((value) => ({
    value,
    label: t(`accounts.backend.${value}`),
  })),
)

function fill(from: ProviderPreset) {
  form.value = { ...from, models: [...from.models] }
  models.value = from.models.join(', ')
}

watch(open, (value) => {
  if (!value) return
  errors.value = {}
  failure.value = ''
  preset.value = ''
  fill(props.provider ?? blank())
})

watch(preset, (id) => {
  const found = providers.presets.find((p) => p.id === id)
  fill(found ?? blank())
})

async function submit() {
  if (busy.value) return
  busy.value = true
  errors.value = {}
  failure.value = ''
  const list = parseModels(models.value)
  try {
    const { name } = form.value
    if (props.provider) {
      await providers.update(props.provider.id, providerChanges(props.provider, form.value, list))
    } else {
      await providers.create(providerInput(form.value, list, preset.value))
    }
    open.value = false
    toast.show({ message: t('accounts.providers.saved', { name }), tone: 'success' })
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
    :title="editing ? t('accounts.providers.editTitle') : t('accounts.providers.addTitle')"
    :dismissible="!busy"
  >
    <form id="provider-form" class="pform" @submit.prevent="submit">
      <Select
        v-if="!editing"
        v-model="preset"
        :label="t('accounts.providers.preset')"
        :options="presetOptions"
      />
      <div class="pform__row">
        <TextField
          v-model="form.id"
          :label="t('accounts.providers.id')"
          :hint="editing ? undefined : t('accounts.providers.idHint')"
          :error="errors.id"
          :disabled="editing"
        />
        <!-- A preset serves one backend; the daemon refuses another one (400 on backend). -->
        <Select
          v-model="form.backend"
          :label="t('accounts.providers.backend')"
          :options="backendOptions"
          :disabled="editing || preset !== ''"
        />
      </div>
      <TextField v-model="form.name" :label="t('accounts.providers.name')" :error="errors.name" />
      <!-- The official API has no base URL: the daemon clears whatever is sent for it. -->
      <TextField
        v-if="!form.official"
        v-model="form.baseUrl"
        type="url"
        :label="t('accounts.providers.baseUrl')"
        placeholder="https://"
        :error="errors.baseUrl"
      />
      <TextField
        v-model="models"
        :label="t('accounts.providers.models')"
        :hint="t('accounts.providers.modelsHint')"
        :error="errors.models"
      />
      <Switch v-model="form.enabled" :label="t('accounts.providers.enabled')" />
      <p v-if="failure" class="pform__error" role="alert">{{ failure }}</p>
    </form>
    <template #footer>
      <Button variant="ghost" :disabled="busy" @click="open = false">
        {{ t('common.action.cancel') }}
      </Button>
      <Button type="submit" form="provider-form" variant="primary" :loading="busy">
        {{ t('accounts.providers.save') }}
      </Button>
    </template>
  </Dialog>
</template>

<style scoped>
.pform {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.pform__row {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: var(--space-3);
}

.pform__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
}
</style>
