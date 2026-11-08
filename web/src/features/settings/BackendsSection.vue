<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import ProviderMark from '@/components/ui/ProviderMark.vue'
import Select from '@/components/ui/Select.vue'
import Switch from '@/components/ui/Switch.vue'

import CommitField from './CommitField.vue'
import SettingRow from './SettingRow.vue'
import { useSettingsForm } from './useSettingsForm'

/**
 * Per backend: whether it takes work, which models it may use (a regular expression) and the model behind each
 * tier; Codex also has its sandbox. Each control saves on its own (backends.<id>.<field> in the merge patch).
 */
defineProps<{ highlight?: string }>()

const { t } = useI18n()
const form = useSettingsForm()

const BACKENDS = [
  { id: 'agy', item: 'backendAgy' },
  { id: 'claude', item: 'backendClaude' },
  { id: 'codex', item: 'backendCodex' },
] as const
const TIERS = ['fast', 'standard', 'strong', 'max'] as const
const SANDBOXES = ['read-only', 'workspace-write', 'danger-full-access'] as const

const text = (path: string) => String(form.get(path) ?? '')
/** "read-only" → "readOnly" (catalog keys have no hyphen). */
const camel = (value: string) => value.replace(/-(\w)/g, (_, c: string) => c.toUpperCase())
const sandboxOptions = () =>
  SANDBOXES.map((value) => ({ value, label: t(`settings.options.sandbox.${camel(value)}`) }))
</script>

<template>
  <p v-if="!form.settings.value" class="be__offline">{{ t('settings.daemon.offline') }}</p>
  <div v-else>
    <SettingRow
      v-for="backend in BACKENDS"
      :key="backend.id"
      :item="backend.item"
      stacked
      :highlighted="highlight === backend.item"
    >
      <template #mark>
        <ProviderMark :backend="backend.id" :label="false" class="be__mark" />
      </template>
      <div class="be">
        <Switch
          :model-value="form.get(`backends.${backend.id}.enabled`) === true"
          :label="t('settings.backends.enabled')"
          @update:model-value="form.set(`backends.${backend.id}.enabled`, $event)"
        />
        <p v-if="form.errors.value[`backends.${backend.id}.enabled`]" class="be__error">
          {{ form.errors.value[`backends.${backend.id}.enabled`] }}
        </p>
        <CommitField
          :model-value="text(`backends.${backend.id}.allowedModels`)"
          :label="t('settings.backends.allowedModels')"
          :hint="t('settings.backends.allowedModelsHint')"
          :placeholder="t('settings.backends.anyModel')"
          :error="form.errors.value[`backends.${backend.id}.allowedModels`]"
          @commit="form.set(`backends.${backend.id}.allowedModels`, $event.trim())"
          @unchanged="form.clear(`backends.${backend.id}.allowedModels`)"
        />
        <div class="be__tiers">
          <CommitField
            v-for="tier in TIERS"
            :key="tier"
            :model-value="text(`backends.${backend.id}.tiers.${tier}`)"
            :label="t(`settings.options.tier.${tier}`)"
            :placeholder="t('settings.backends.tierDefault')"
            :error="form.errors.value[`backends.${backend.id}.tiers.${tier}`]"
            @commit="form.set(`backends.${backend.id}.tiers.${tier}`, $event.trim())"
            @unchanged="form.clear(`backends.${backend.id}.tiers.${tier}`)"
          />
        </div>
        <template v-if="backend.id === 'codex'">
          <Select
            :model-value="text('backends.codex.sandbox')"
            :label="t('settings.backends.sandbox')"
            :hint="t('settings.backends.sandboxHint')"
            size="sm"
            :options="sandboxOptions()"
            @update:model-value="form.set('backends.codex.sandbox', $event)"
          />
          <p v-if="form.errors.value['backends.codex.sandbox']" class="be__error">
            {{ form.errors.value['backends.codex.sandbox'] }}
          </p>
        </template>
        <p class="be__state" aria-live="polite">
          {{
            [...form.saving.value].some((p) => p.startsWith(`backends.${backend.id}.`))
              ? t('settings.daemon.saving')
              : [...form.saved.value].some((p) => p.startsWith(`backends.${backend.id}.`))
                ? t('settings.daemon.saved')
                : ''
          }}
        </p>
      </div>
    </SettingRow>
  </div>
</template>

<style scoped>
.be__mark {
  margin-right: var(--space-2);
}

.be {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  margin-top: var(--space-2);
}

.be__tiers {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 180px), 1fr));
  gap: var(--space-2);
}

.be__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-xs);
}

.be__state {
  min-height: 1lh;
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.be__offline {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}
</style>
