<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import type { TierModels } from '@/api/types'
import Select from '@/components/ui/Select.vue'

import TierEditor from './TierEditor.vue'

/**
 * "Model workers use": one dropdown whose first choice, "Let the lead choose (any model)", leaves every tier
 * empty (each task names its model). Picking a model sets all four tiers to it; "Different model per tier"
 * opens the four tier fields. `models` are the ids the service is known to have (its tiers, its list, a key test).
 */
const tiers = defineModel<TierModels>({ required: true })
const props = defineProps<{ models: readonly string[]; disabled?: boolean; hint?: string }>()

const { t } = useI18n()
const id = useId()

/** Select values: '' lets the lead choose, PER_TIER stands for tiers that differ. */
const PER_TIER = '\u0000tiers'
const values = computed(() => [
  tiers.value.fast,
  tiers.value.standard,
  tiers.value.strong,
  tiers.value.max,
])
const current = computed(() => {
  const [first] = values.value
  if (values.value.every((v) => v === '')) return ''
  return values.value.every((v) => v === first) ? (first ?? '') : PER_TIER
})
const perTierOpen = ref(current.value === PER_TIER)

const options = computed(() => {
  const models = [...new Set([...props.models, ...values.value].filter(Boolean))]
  return [
    { value: '', label: t('accounts.modelPicker.lead') },
    ...models.map((m) => ({ value: m, label: m })),
    ...(current.value === PER_TIER
      ? [{ value: PER_TIER, label: t('accounts.modelPicker.perTierOption') }]
      : []),
  ]
})

const selected = computed({
  get: () => current.value,
  set: (value: string) => {
    if (value === PER_TIER) {
      perTierOpen.value = true
      return
    }
    tiers.value = { fast: value, standard: value, strong: value, max: value }
  },
})

const hintText = computed(
  () => props.hint ?? (current.value === '' ? t('accounts.modelPicker.leadHint') : undefined),
)
</script>

<template>
  <div class="models">
    <Select
      v-model="selected"
      :label="t('accounts.modelPicker.label')"
      :options="options"
      :hint="hintText"
      :disabled="disabled"
    />
    <button
      v-if="!disabled"
      type="button"
      class="models__toggle"
      :aria-expanded="perTierOpen"
      :aria-controls="`${id}-tiers`"
      @click="perTierOpen = !perTierOpen"
    >
      {{ t('accounts.modelPicker.perTier') }}
    </button>
    <div v-if="perTierOpen && !disabled" :id="`${id}-tiers`">
      <TierEditor
        v-model="tiers"
        :suggestions="models"
        :legend="t('accounts.modelPicker.perTier')"
      />
    </div>
  </div>
</template>

<style scoped>
.models {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.models__toggle {
  align-self: flex-start;
  padding: 0;
  border: 0;
  background: none;
  color: var(--link);
  font: inherit;
  font-size: var(--fs-xs);
  text-decoration: underline;
  cursor: pointer;
}
</style>
