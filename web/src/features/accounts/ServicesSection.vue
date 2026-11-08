<script setup lang="ts">
import { computed, onMounted, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import { ApiError, errorMessage } from '@/api/client'
import type { Provider } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import ConfirmDialog from '@/components/ui/ConfirmDialog.vue'
import Spinner from '@/components/ui/Spinner.vue'
import TextField from '@/components/ui/TextField.vue'
import { useAccountsStore } from '@/stores/accounts'
import { useProvidersStore } from '@/stores/providers'
import { useToastStore } from '@/stores/toast'

import { useActions } from './accountView'
import AddAccountDialog from './AddAccountDialog.vue'
import { serviceValue } from './apiKey'
import { allowList } from './providerForm'
import PricesDialog from './PricesDialog.vue'
import ProviderDialog from './ProviderDialog.vue'
import ServiceItem from './ServiceItem.vue'

/**
 * Services (the API providers): where API keys send their requests. Advanced, so on the Accounts page it starts
 * collapsed; Settings shows it open and under its own title (`collapsible` and `heading` false). Each service shows its address, its model and tier
 * models (editable in its dialog), how many keys use it, and "Add key", which opens the add form with that service
 * chosen. A service that keys still use cannot be deleted: the confirmation names those accounts, and the
 * daemon's refusal is shown if it comes anyway.
 */
withDefaults(defineProps<{ collapsible?: boolean; heading?: boolean }>(), {
  collapsible: true,
  heading: true,
})

const { t } = useI18n()
const providers = useProvidersStore()
const accounts = useAccountsStore()
const toast = useToastStore()
const actions = useActions()

const id = useId()
const expanded = ref(false)
const query = ref('')
const editing = ref<Provider | null>(null)
const dialogOpen = ref(false)
const adding = ref(false)
const addService = ref('')
const deleting = ref<Provider | null>(null)
const pricing = ref<Provider | null>(null)
const deleteBusy = ref(false)
const deleteError = ref('')

onMounted(() => providers.ensure())

const shown = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return providers.items
  return providers.items.filter((p) => {
    const allow = allowList(p)
    const models = allow.kind === 'list' ? allow.models.join(' ') : ''
    return [p.id, p.name, p.baseUrl, p.backend, models].some((s) => s.toLowerCase().includes(q))
  })
})

/** Accounts per service, from the live account list (the count follows an account write at once). */
const users = computed(() => {
  const map = new Map<string, string[]>()
  for (const a of accounts.accounts)
    if (a.provider) map.set(a.provider, [...(map.get(a.provider) ?? []), a.label])
  return map
})
const usersOf = (p: Provider | null) => (p ? (users.value.get(p.id) ?? []) : [])

function edit(provider: Provider | null) {
  editing.value = provider
  dialogOpen.value = true
}

function addKey(provider: Provider) {
  addService.value = serviceValue({ kind: 'provider', id: provider.id })
  adding.value = true
}

function askDelete(provider: Provider) {
  deleteError.value = ''
  deleting.value = provider
}

const deleteDesc = computed(() => {
  const names = usersOf(deleting.value)
  return names.length
    ? t('accounts.services.usedByNames', { names: names.join(', ') }, names.length)
    : t('accounts.providers.deleteDesc')
})

async function remove() {
  const provider = deleting.value
  if (!provider) return
  deleteBusy.value = true
  try {
    await providers.remove(provider.id)
    toast.show({
      message: t('accounts.providers.deleted', { name: provider.name }),
      tone: 'success',
    })
    deleting.value = null
  } catch (error) {
    deleteError.value =
      error instanceof ApiError && error.status === 409
        ? t('accounts.providers.inUse', { error: error.message })
        : errorMessage(error)
  } finally {
    deleteBusy.value = false
  }
}

function setEnabled(provider: Provider, enabled: boolean) {
  void actions.run(provider.id, () => providers.update(provider.id, { enabled }))
}
</script>

<template>
  <section
    class="services"
    :aria-labelledby="heading ? `${id}-title` : undefined"
    :aria-label="heading ? undefined : t('accounts.providers.title')"
  >
    <div class="services__head">
      <div v-if="heading">
        <h2 :id="`${id}-title`" class="services__title">
          <button
            v-if="collapsible"
            type="button"
            class="services__toggle"
            :aria-expanded="expanded"
            :aria-controls="`${id}-body`"
            @click="expanded = !expanded"
          >
            {{ t('accounts.providers.title') }}
          </button>
          <template v-else>{{ t('accounts.providers.title') }}</template>
          <span class="services__badge">{{ t('accounts.services.advanced') }}</span>
        </h2>
        <p class="services__desc">{{ t('accounts.providers.desc') }}</p>
      </div>
      <Button
        v-if="!collapsible || expanded"
        size="sm"
        variant="secondary"
        icon="plus"
        @click="edit(null)"
      >
        {{ t('accounts.providers.add') }}
      </Button>
    </div>

    <div v-if="!collapsible || expanded" :id="`${id}-body`" class="services__body">
      <TextField
        v-if="providers.items.length > 3"
        v-model="query"
        type="search"
        icon="search"
        size="sm"
        :label="t('accounts.providers.search')"
        hide-label
        :placeholder="t('accounts.providers.search')"
      />

      <Spinner v-if="providers.status === 'loading'" :label="t('accounts.providers.loading')" />
      <p v-else-if="providers.status === 'error'" class="services__empty" role="alert">
        {{ t('accounts.providers.loadFailed', { error: providers.error }) }}
        <Button size="sm" variant="ghost" @click="providers.load()">
          {{ t('common.action.retry') }}
        </Button>
      </p>
      <p v-else-if="!shown.length" class="services__empty">
        {{ query ? t('accounts.providers.noMatch') : t('accounts.providers.empty') }}
      </p>
      <ul v-else class="services__list">
        <ServiceItem
          v-for="provider in shown"
          :key="provider.id"
          :provider="provider"
          :keys="usersOf(provider).length"
          :busy="actions.isBusy(provider.id)"
          @add-key="addKey(provider)"
          @prices="pricing = provider"
          @edit="edit(provider)"
          @remove="askDelete(provider)"
          @enabled="setEnabled(provider, $event)"
        />
      </ul>
    </div>

    <ProviderDialog v-model:open="dialogOpen" :provider="editing" />
    <AddAccountDialog v-model:open="adding" :service="addService" />
    <PricesDialog :open="pricing !== null" :provider="pricing" @update:open="pricing = null" />
    <ConfirmDialog
      :open="deleting !== null"
      :title="t('accounts.providers.deleteTitle', { name: deleting?.name ?? '' })"
      :description="deleteDesc"
      :confirm-label="t('accounts.providers.deleteConfirm')"
      danger
      :busy="deleteBusy"
      :error="deleteError"
      @update:open="deleting = null"
      @confirm="remove"
    />
  </section>
</template>

<style scoped>
.services {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.services__head {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
}

.services__title {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
}

.services__toggle {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: 0;
  border: 0;
  background: none;
  color: inherit;
  font: inherit;
  cursor: pointer;
}

.services__toggle::before {
  content: '▸';
  color: var(--text-muted);
  font-size: var(--fs-sm);
  transition: transform var(--transition-fast);
}

.services__toggle[aria-expanded='true']::before {
  transform: rotate(90deg);
}

.services__toggle:focus-visible {
  outline: 2px solid var(--border-focus);
  outline-offset: 2px;
}

.services__badge {
  padding: 0 var(--space-2);
  border-radius: var(--radius-pill);
  background: var(--neutral-bg);
  color: var(--neutral-fg);
  font-size: var(--fs-2xs);
  font-weight: var(--fw-medium);
  line-height: 20px;
}

.services__desc,
.services__empty {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.services__body {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.services__list {
  display: flex;
  flex-direction: column;
  margin: 0;
  padding: 0;
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
  list-style: none;
}
</style>
