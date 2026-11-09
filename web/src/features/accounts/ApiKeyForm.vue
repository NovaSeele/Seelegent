<script setup lang="ts">
import { computed, nextTick, onMounted, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { AccountCreated } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Checkbox from '@/components/ui/Checkbox.vue'
import NumberField from '@/components/ui/NumberField.vue'
import Select from '@/components/ui/Select.vue'
import TextField from '@/components/ui/TextField.vue'

import ApiKeyField from './ApiKeyField.vue'
import KeyTestNote from './KeyTestNote.vue'
import ModelPicker from './ModelPicker.vue'
import ServicePicker from './ServicePicker.vue'
import { useApiKeyForm } from './useApiKeyForm'

/**
 * Step 2 of adding an account with an API key: the service, the key (focused first), the model, and under "More
 * options" the name, priority and budget, under "Advanced" the address, wire API and extra models. The dialog's
 * footer drives it through `submit` (exposed with its state): "Test and add", then "Add without testing" after a
 * failed test. See useApiKeyForm for the rules.
 */
const props = withDefaults(defineProps<{ formId: string; service?: string }>(), { service: '' })
const emit = defineEmits<{ done: [created: AccountCreated] }>()

const { t } = useI18n()
const id = useId()
const form = useApiKeyForm(props.service, (created) => emit('done', created))
const {
  service: chosenService,
  rawKey,
  tiers,
  label,
  priority,
  budget,
  budgetStop,
  baseUrl,
  wireApi,
  extraModels,
  busy,
  testing,
  attempted,
  result,
  notices,
  errors,
  failure,
  info,
  existing,
  preset,
  knownModels,
  serviceError,
  baseUrlError,
  keyError,
  valid,
  failed,
  detected,
  labelTouched,
  serviceTouched,
  custom,
} = form
const keyField = ref<InstanceType<typeof ApiKeyField> | null>(null)
const moreOpen = ref(false)
const advancedOpen = ref(false)

const wireOptions = computed(() => [
  { value: 'chat', label: t('accounts.form.wireChat') },
  { value: 'responses', label: t('accounts.form.wireResponses') },
])
const modelHint = computed(() =>
  existing.value
    ? t('accounts.modelPicker.fromService', { service: info.value?.name ?? '' })
    : undefined,
)

function onTouched() {
  serviceTouched.value = true
  detected.value = ''
}

async function submit(withTest: boolean) {
  await form.submit(withTest)
  // A field the daemon refused is shown where it is; open its section so it can be seen.
  if (errors.value['newProvider.baseUrl']) advancedOpen.value = true
  if (errors.value.label || errors.value.priority || errors.value.budgetUsd) moreOpen.value = true
  if (attempted.value && !valid.value) {
    if (baseUrlError.value) advancedOpen.value = true
    await nextTick()
    keyField.value?.focus()
  }
}

// A hand-made service needs its address: open the section that asks for it.
watch(custom, (isCustom) => {
  if (isCustom) advancedOpen.value = true
})

onMounted(() => keyField.value?.focus())

defineExpose({ submit, busy, failed, valid })
</script>

<template>
  <form :id="formId" class="keyform" novalidate @submit.prevent="submit(true)">
    <ServicePicker
      v-model="chosenService"
      :error="serviceError || undefined"
      @touched="onTouched"
    />
    <ApiKeyField
      ref="keyField"
      v-model="rawKey"
      :error="keyError || undefined"
      :detected="detected || undefined"
      :show-empty="attempted"
    />
    <div class="keyform__models">
      <ModelPicker v-model="tiers" :models="knownModels" :disabled="!!existing" :hint="modelHint" />
      <Button
        v-if="!existing && info"
        size="sm"
        variant="ghost"
        icon="refresh"
        :loading="testing && !busy"
        :disabled="busy"
        @click="form.listModels()"
      >
        {{ t('accounts.form.listModels') }}
      </Button>
    </div>

    <KeyTestNote :result="result" :notices="notices" :testing="testing" />

    <section class="keyform__section">
      <button
        type="button"
        class="keyform__disclosure"
        :aria-expanded="moreOpen"
        :aria-controls="`${id}-more`"
        @click="moreOpen = !moreOpen"
      >
        {{ t('accounts.form.more') }}
      </button>
      <div v-show="moreOpen" :id="`${id}-more`" class="keyform__group">
        <TextField
          v-model="label"
          :label="t('accounts.add.label')"
          :error="errors.label"
          @update:model-value="labelTouched = true"
        />
        <NumberField
          :model-value="priority"
          :label="t('accounts.priority')"
          :hint="t('accounts.add.priorityHint')"
          :error="errors.priority"
          integer
          @commit="priority = $event"
        />
        <NumberField
          :model-value="budget"
          :label="t('accounts.form.budget')"
          :hint="t('accounts.form.budgetHint')"
          :error="errors.budgetUsd"
          unit="USD"
          @commit="budget = Math.max(0, $event)"
        />
        <Checkbox
          v-model="budgetStop"
          :label="t('accounts.form.budgetStop')"
          :disabled="budget <= 0"
        />
      </div>
    </section>

    <section v-if="preset && !preset.official" class="keyform__section">
      <button
        type="button"
        class="keyform__disclosure"
        :aria-expanded="advancedOpen"
        :aria-controls="`${id}-advanced`"
        @click="advancedOpen = !advancedOpen"
      >
        {{ t('accounts.form.advanced') }}
      </button>
      <div v-show="advancedOpen" :id="`${id}-advanced`" class="keyform__group">
        <TextField
          v-model="baseUrl"
          type="url"
          :label="t('accounts.form.baseUrl')"
          :hint="t('accounts.form.baseUrlHint')"
          placeholder="https://"
          :error="baseUrlError || undefined"
        />
        <Select
          v-if="preset?.backend === 'codex'"
          v-model="wireApi"
          :label="t('accounts.form.wireApi')"
          :options="wireOptions"
        />
        <TextField
          v-model="extraModels"
          :label="t('accounts.form.extraModels')"
          :hint="t('accounts.form.extraModelsHint')"
          :error="errors['newProvider.models']"
        />
      </div>
    </section>

    <p v-if="failure" class="keyform__error" role="alert">{{ failure }}</p>
  </form>
</template>

<style scoped>
.keyform {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.keyform__models {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--space-1);
}

.keyform__models > :first-child {
  align-self: stretch;
}

.keyform__section {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.keyform__disclosure {
  align-self: flex-start;
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: 0;
  border: 0;
  background: none;
  color: var(--text-primary);
  font: inherit;
  font-size: var(--fs-sm);
  font-weight: var(--fw-medium);
  cursor: pointer;
}

.keyform__disclosure::before {
  content: '▸';
  color: var(--text-muted);
  transition: transform var(--transition-fast);
}

.keyform__disclosure[aria-expanded='true']::before {
  transform: rotate(90deg);
}

.keyform__disclosure:focus-visible {
  outline: 2px solid var(--border-focus);
  outline-offset: 2px;
}

.keyform__group {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding-inline-start: var(--space-4);
  border-inline-start: var(--border-width) solid var(--border-subtle);
}

.keyform__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
}
</style>
