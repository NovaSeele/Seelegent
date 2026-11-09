<script setup lang="ts">
import { useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { TierModels } from '@/api/types'
import TextField from '@/components/ui/TextField.vue'

/**
 * The model of each tier (fast, standard, strong, max) of a service, as four text fields that suggest the models
 * the service is known to have; any id can be typed. An empty tier means tasks of that tier must name their model.
 */
const tiers = defineModel<TierModels>({ required: true })
defineProps<{ suggestions: readonly string[]; legend: string }>()

const { t } = useI18n()
const id = useId()

const TIERS = ['fast', 'standard', 'strong', 'max'] as const

// Edits build on the last value sent, not on the prop: two fields changed in one tick (autofill) both count.
let latest = tiers.value
watch(tiers, (value) => (latest = value))

function set(tier: (typeof TIERS)[number], value: string) {
  latest = { ...latest, [tier]: value.trim() }
  tiers.value = latest
}
</script>

<template>
  <fieldset class="tiers">
    <legend class="tiers__legend">{{ legend }}</legend>
    <datalist :id="`${id}-models`">
      <option v-for="m in suggestions" :key="m" :value="m" />
    </datalist>
    <div class="tiers__grid">
      <TextField
        v-for="tier in TIERS"
        :key="tier"
        :model-value="tiers[tier]"
        size="sm"
        :label="t(`accounts.modelPicker.tier.${tier}`)"
        :list="`${id}-models`"
        :placeholder="t('accounts.modelPicker.tierEmpty')"
        @update:model-value="set(tier, $event)"
      />
    </div>
    <p class="tiers__hint">{{ t('accounts.modelPicker.tierHint') }}</p>
  </fieldset>
</template>

<style scoped>
.tiers {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}

.tiers__legend {
  margin-bottom: var(--space-1);
  padding: 0;
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.tiers__grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
  gap: var(--space-2) var(--space-3);
}

.tiers__hint {
  color: var(--text-muted);
  font-size: var(--fs-2xs);
}
</style>
