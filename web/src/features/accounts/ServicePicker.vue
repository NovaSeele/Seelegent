<script setup lang="ts">
import { computed, nextTick, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import ProviderMark from '@/components/ui/ProviderMark.vue'
import TextField from '@/components/ui/TextField.vue'
import { useAccountsStore } from '@/stores/accounts'
import { useProvidersStore } from '@/stores/providers'

import { orderedPresets, parseService, serviceValue } from './apiKey'
import ServiceChosen from './ServiceChosen.vue'
import { useServiceNames } from './useServiceNames'

/**
 * Which service an API key is from: the user's own services first (with how many keys use each), then the
 * presets, searchable. The backend is never asked: a line under the choice names the CLI it runs through. While
 * nothing is chosen the list is open; a choice made with the pointer closes it to one line with "Change" (keys
 * only move the choice, so arrowing through the list never closes it; Enter closes it instead of submitting).
 * Values are "provider:<id>" or "preset:<id>" (apiKey.ts).
 */
const model = defineModel<string>({ required: true })
defineProps<{ error?: string }>()
const emit = defineEmits<{ touched: [] }>()

const { t } = useI18n()
const providers = useProvidersStore()
const accounts = useAccountsStore()
const names = useServiceNames()
const id = useId()

const open = ref(model.value === '')
const query = ref('')
const listEl = ref<HTMLElement | null>(null)

// Cleared: open the list. Chosen from outside it (a pasted key named its service): show the one line.
watch(model, (value) => {
  if (value === '') open.value = true
  else if (!listEl.value?.contains(document.activeElement)) open.value = false
})

const keyCounts = computed(() => {
  const counts = new Map<string, number>()
  for (const a of accounts.accounts)
    if (a.provider) counts.set(a.provider, (counts.get(a.provider) ?? 0) + 1)
  return counts
})

const matches = (...texts: string[]) => {
  const q = query.value.trim().toLowerCase()
  return !q || texts.some((s) => s.toLowerCase().includes(q))
}

const yours = computed(() =>
  providers.items
    .filter((p) => p.enabled && matches(p.id, p.name, p.baseUrl))
    .map((p) => ({
      value: serviceValue({ kind: 'provider', id: p.id }),
      name: p.name,
      backend: p.backend,
      meta: t(
        'accounts.service.keys',
        { n: keyCounts.value.get(p.id) ?? 0 },
        keyCounts.value.get(p.id) ?? 0,
      ),
    })),
)
const presets = computed(() =>
  orderedPresets(providers.presets)
    .map((p) => ({
      value: serviceValue({ kind: 'preset', id: p.id }),
      name: names.preset(p),
      backend: p.backend,
      meta: '',
      search: [p.id, p.name, p.baseUrl],
    }))
    .filter((p) => matches(p.name, ...p.search)),
)

const chosen = computed(() => {
  const ref = parseService(model.value)
  return ref ? names.of(ref) : null
})

function choose(event: MouseEvent) {
  emit('touched')
  // Set here, not left to the radio's change event: in a browser the list may close (and the radio go) before it.
  if (event.currentTarget instanceof HTMLInputElement) model.value = event.currentTarget.value
  // A real pointer click (detail > 0) closes the list; the click a key press makes (Space, arrows) does not.
  if (event.detail > 0) void nextTick(() => (open.value = false))
}

async function reopen() {
  open.value = true
  await nextTick()
  listEl.value?.querySelector<HTMLInputElement>('input[type="radio"]:checked')?.focus()
}

function onKeydown(event: KeyboardEvent) {
  if (event.key !== 'Enter' || !model.value) return
  event.preventDefault()
  open.value = false
}
</script>

<template>
  <div class="picker">
    <ServiceChosen
      v-if="!open && chosen"
      :name="chosen.name"
      :backend="chosen.backend"
      @change="reopen"
    />
    <fieldset v-else class="picker__set" :aria-describedby="error ? `${id}-error` : undefined">
      <legend class="picker__label">{{ t('accounts.service.label') }}</legend>
      <TextField
        v-if="providers.items.length + providers.presets.length > 6"
        v-model="query"
        type="search"
        icon="search"
        size="sm"
        :label="t('accounts.service.search')"
        hide-label
        :placeholder="t('accounts.service.search')"
      />
      <div ref="listEl" class="picker__list" @keydown="onKeydown">
        <template
          v-for="group in [
            { key: 'yours', items: yours },
            { key: 'presets', items: presets },
          ]"
          :key="group.key"
        >
          <div v-if="group.items.length" role="group" :aria-labelledby="`${id}-${group.key}`">
            <p :id="`${id}-${group.key}`" class="picker__group">
              {{ t(`accounts.service.${group.key}`) }}
            </p>
            <label
              v-for="item in group.items"
              :key="item.value"
              class="picker__item"
              :class="{ 'picker__item--on': model === item.value }"
            >
              <input
                v-model="model"
                type="radio"
                class="picker__radio"
                :name="id"
                :value="item.value"
                @click="choose"
              />
              <ProviderMark :backend="item.backend" :label="false" />
              <span class="picker__name">{{ item.name }}</span>
              <span v-if="item.meta" class="picker__meta">{{ item.meta }}</span>
            </label>
          </div>
        </template>
        <p v-if="!yours.length && !presets.length" class="picker__empty">
          {{ t('accounts.service.noMatch') }}
        </p>
      </div>
      <p v-if="chosen" class="picker__cli">
        {{ t('accounts.service.runsOn', { cli: t(`accounts.backend.${chosen.backend}`) }) }}
      </p>
      <p v-if="error" :id="`${id}-error`" class="picker__error">{{ error }}</p>
    </fieldset>
  </div>
</template>

<style scoped>
.picker__set {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}

.picker__label {
  padding: 0;
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.picker__cli {
  margin: 0;
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.picker__list {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  max-height: 260px;
  overflow-y: auto;
  padding: var(--space-1);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-md);
}

.picker__group {
  margin: 0;
  padding: var(--space-1) var(--space-2) 0;
  color: var(--text-muted);
  font-size: var(--fs-2xs);
  font-weight: var(--fw-medium);
  text-transform: uppercase;
}

.picker__item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: var(--target-min);
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm);
  font-size: var(--fs-sm);
  cursor: pointer;
}

.picker__item:hover {
  background: var(--bg-hover);
  --text-muted: var(--text-primary);
  --text-secondary: var(--text-primary);
}

.picker__item--on {
  background: var(--accent-soft);
  --text-muted: var(--text-primary);
  --text-secondary: var(--text-primary);
}

.picker__item:has(.picker__radio:focus-visible) {
  outline: 2px solid var(--border-focus);
  outline-offset: -2px;
}

.picker__radio {
  flex: none;
  margin: 0;
  accent-color: var(--accent);
}

.picker__name {
  min-width: 0;
  overflow-wrap: anywhere;
}

.picker__meta {
  margin-left: auto;
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  white-space: nowrap;
}

.picker__empty {
  padding: var(--space-2);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.picker__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-xs);
}
</style>
