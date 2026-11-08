<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Provider } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import IconButton from '@/components/ui/IconButton.vue'
import ProviderMark from '@/components/ui/ProviderMark.vue'
import Switch from '@/components/ui/Switch.vue'

import { allowList, tierModels } from './providerForm'

/**
 * One service of the Services section: its name and CLI, its address, the model workers use (its tier models, or
 * "chosen per task"), the models it allows, how many keys use it ("No key yet"), and its controls: Add key, on/off,
 * edit (address, models and tier models) and delete.
 */
const props = defineProps<{ provider: Provider; keys: number; busy: boolean }>()
const emit = defineEmits<{
  addKey: []
  prices: []
  edit: []
  remove: []
  enabled: [value: boolean]
}>()

const { t } = useI18n()

const address = computed(() =>
  props.provider.official ? t('accounts.services.official') : props.provider.baseUrl,
)
const model = computed(() => {
  const models = tierModels(props.provider)
  if (!models.length) return t('accounts.modelPerTask')
  return models.length === 1
    ? t('accounts.facts.model', { model: models[0] })
    : t('accounts.facts.models', { models: models.join(', ') })
})
const allowed = computed(() => {
  const allow = allowList(props.provider)
  if (allow.kind === 'list') return allow.models.join(', ')
  if (allow.kind === 'pattern')
    return t('accounts.providers.patternModels', { pattern: allow.pattern })
  return t('accounts.providers.anyModel')
})
</script>

<template>
  <li class="svc">
    <div class="svc__main">
      <p class="svc__name">
        {{ provider.name }}
        <ProviderMark :backend="provider.backend" size="sm" class="svc__backend" />
      </p>
      <p class="svc__url">{{ address }}</p>
      <p class="svc__meta">
        <span>{{ model }}</span>
        <span>{{ t('accounts.services.allows', { models: allowed }) }}</span>
        <span :class="{ svc__none: keys === 0 }">
          {{ keys ? t('accounts.service.keys', { n: keys }, keys) : t('accounts.service.noKey') }}
        </span>
      </p>
    </div>
    <div class="svc__actions">
      <Button size="sm" variant="secondary" icon="plus" @click="emit('addKey')">
        {{ t('accounts.services.addKey') }}
      </Button>
      <Button
        v-if="provider.spendSource !== 'free'"
        size="sm"
        variant="ghost"
        icon="quota"
        :aria-label="t('accounts.services.pricesOf', { name: provider.name })"
        @click="emit('prices')"
      >
        {{ t('accounts.services.prices') }}
      </Button>
      <Switch
        :model-value="provider.enabled"
        :label="t('accounts.providers.enabledOf', { name: provider.name })"
        hide-label
        :disabled="busy"
        @update:model-value="emit('enabled', $event)"
      />
      <IconButton
        icon="settings"
        size="sm"
        :label="t('accounts.providers.editOf', { name: provider.name })"
        @click="emit('edit')"
      />
      <IconButton
        icon="trash"
        size="sm"
        :label="t('accounts.providers.deleteOf', { name: provider.name })"
        @click="emit('remove')"
      />
    </div>
  </li>
</template>

<style scoped>
.svc {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-3);
  padding: var(--space-3) var(--space-4);
}

.svc + .svc {
  border-top: var(--border-width) solid var(--border-subtle);
}

.svc__main {
  flex: 1 1 260px;
  min-width: 0;
}

.svc__name {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--fs-sm);
  font-weight: var(--fw-medium);
}

.svc__backend {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-regular);
}

.svc__url {
  overflow: hidden;
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.svc__meta {
  display: flex;
  flex-wrap: wrap;
  gap: 0 var(--space-2);
  color: var(--text-muted);
  font-size: var(--fs-2xs);
}

.svc__meta > span + span::before {
  margin-right: var(--space-2);
  content: '·';
}

.svc__none {
  color: var(--text-secondary);
  font-weight: var(--fw-medium);
}

.svc__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) var(--space-2);
}
</style>
