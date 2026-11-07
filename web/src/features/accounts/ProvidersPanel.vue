<script setup lang="ts">
import { computed, onMounted, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import { ApiError, errorMessage } from '@/api/client'
import type { Provider } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import ConfirmDialog from '@/components/ui/ConfirmDialog.vue'
import IconButton from '@/components/ui/IconButton.vue'
import Spinner from '@/components/ui/Spinner.vue'
import Switch from '@/components/ui/Switch.vue'
import TextField from '@/components/ui/TextField.vue'
import { useAccountsStore } from '@/stores/accounts'
import { useProvidersStore } from '@/stores/providers'
import { useToastStore } from '@/stores/toast'

import { useActions } from './accountView'
import ProviderDialog from './ProviderDialog.vue'
import { allowList } from './providerForm'

/**
 * The API providers API-key accounts call (Anthropic-compatible for Claude Code, OpenAI-compatible for Codex):
 * searchable (old bug B25), each with its base URL, models and how many accounts use it. A provider in use cannot
 * be deleted (409), which the confirmation says. Shown on the Accounts page and in Settings.
 */
withDefaults(defineProps<{ heading?: boolean }>(), { heading: true })

const { t } = useI18n()
const providers = useProvidersStore()
const accounts = useAccountsStore()
const toast = useToastStore()
const actions = useActions()

const titleId = useId()
const query = ref('')
const editing = ref<Provider | null>(null)
const dialogOpen = ref(false)
const deleting = ref<Provider | null>(null)
const deleteBusy = ref(false)
const deleteError = ref('')

onMounted(() => providers.ensure())

const shown = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return providers.items
  return providers.items.filter((p) =>
    [p.id, p.name, p.baseUrl, p.backend, modelsText(p)].some((s) => s.toLowerCase().includes(q)),
  )
})

/**
 * Accounts using each provider, counted from the live account list: the daemon sends no providers.changed when
 * an account is added or deleted, so Provider.accounts goes stale until the next providers read.
 */
const usage = computed(() => {
  const counts = new Map<string, number>()
  for (const a of accounts.accounts)
    if (a.provider) counts.set(a.provider, (counts.get(a.provider) ?? 0) + 1)
  return counts
})
const usedBy = (p: Provider) => usage.value.get(p.id) ?? 0

/** The models a provider allows: its list, its hand-written pattern, or any model when neither is set. */
function modelsText(p: Provider): string {
  const allow = allowList(p)
  if (allow.kind === 'list') return allow.models.join(', ')
  if (allow.kind === 'pattern')
    return t('accounts.providers.patternModels', { pattern: allow.pattern })
  return t('accounts.providers.anyModel')
}

function edit(provider: Provider | null) {
  editing.value = provider
  dialogOpen.value = true
}

function askDelete(provider: Provider) {
  deleteError.value = ''
  deleting.value = provider
}

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
    class="providers"
    :aria-labelledby="heading ? titleId : undefined"
    :aria-label="heading ? undefined : t('accounts.providers.title')"
  >
    <div class="providers__head">
      <div v-if="heading">
        <h2 :id="titleId" class="providers__title">{{ t('accounts.providers.title') }}</h2>
        <p class="providers__desc">{{ t('accounts.providers.desc') }}</p>
      </div>
      <Button size="sm" variant="secondary" icon="plus" @click="edit(null)">
        {{ t('accounts.providers.add') }}
      </Button>
    </div>

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
    <p v-else-if="providers.status === 'error'" class="providers__empty" role="alert">
      {{ t('accounts.providers.loadFailed', { error: providers.error }) }}
      <Button size="sm" variant="ghost" @click="providers.load()">
        {{ t('common.action.retry') }}
      </Button>
    </p>
    <p v-else-if="!shown.length" class="providers__empty">
      {{ query ? t('accounts.providers.noMatch') : t('accounts.providers.empty') }}
    </p>
    <ul v-else class="providers__list">
      <li v-for="provider in shown" :key="provider.id" class="prov">
        <div class="prov__main">
          <p class="prov__name">
            {{ provider.name }}
            <span class="prov__backend">{{ t(`accounts.backend.${provider.backend}`) }}</span>
          </p>
          <p class="prov__url">{{ provider.baseUrl }}</p>
          <p class="prov__meta">
            {{ t('accounts.providers.usedBy', { n: usedBy(provider) }, usedBy(provider)) }}
            ·
            {{ modelsText(provider) }}
          </p>
        </div>
        <div class="prov__actions">
          <Switch
            :model-value="provider.enabled"
            :label="t('accounts.providers.enabledOf', { name: provider.name })"
            hide-label
            :disabled="actions.isBusy(provider.id)"
            @update:model-value="setEnabled(provider, $event)"
          />
          <IconButton
            icon="settings"
            size="sm"
            :label="t('accounts.providers.editOf', { name: provider.name })"
            @click="edit(provider)"
          />
          <IconButton
            icon="x"
            size="sm"
            :label="t('accounts.providers.deleteOf', { name: provider.name })"
            @click="askDelete(provider)"
          />
        </div>
      </li>
    </ul>

    <ProviderDialog v-model:open="dialogOpen" :provider="editing" />
    <ConfirmDialog
      :open="deleting !== null"
      :title="t('accounts.providers.deleteTitle', { name: deleting?.name ?? '' })"
      :description="t('accounts.providers.deleteDesc')"
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
.providers {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.providers__head {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
}

.providers__title {
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
}

.providers__desc,
.providers__empty {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.providers__list {
  display: flex;
  flex-direction: column;
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
  margin: 0;
  padding: 0;
  list-style: none;
}

.prov {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-3);
  padding: var(--space-3) var(--space-4);
}

.prov + .prov {
  border-top: var(--border-width) solid var(--border-subtle);
}

.prov__main {
  flex: 1 1 240px;
  min-width: 0;
}

.prov__name {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--fs-sm);
  font-weight: var(--fw-medium);
}

.prov__backend {
  padding: 0 var(--space-2);
  border-radius: var(--radius-pill);
  background: var(--neutral-bg);
  color: var(--neutral-fg);
  font-size: var(--fs-2xs);
  font-weight: var(--fw-regular);
  line-height: 18px;
}

.prov__url {
  overflow: hidden;
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.prov__meta {
  color: var(--text-muted);
  font-size: var(--fs-2xs);
}

.prov__actions {
  display: flex;
  align-items: center;
  gap: var(--space-1);
}
</style>
