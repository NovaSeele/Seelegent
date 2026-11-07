<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage, fieldProblems } from '@/api/client'
import type { HookConfig } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useSettingsStore } from '@/stores/settings'
import { useToastStore } from '@/stores/toast'

import HookCard from './HookCard.vue'
import SettingRow from './SettingRow.vue'

/**
 * Hooks: shell commands the daemon runs when events happen. The list is edited as a whole and saved with one
 * button (the daemon replaces the list); Discard goes back to the saved list. A hook.ran frame updates a hook's
 * last run without touching unsaved edits.
 */
defineProps<{ highlight?: string }>()

const { t } = useI18n()
const settings = useSettingsStore()
const toast = useToastStore()

const draft = ref<HookConfig[]>([])
const dirty = ref(false)
const saving = ref(false)
const errors = ref<Record<string, string>>({})
const failure = ref('')

const toConfig = (list: readonly HookConfig[]): HookConfig[] =>
  list.map(({ id, on, run, project, timeoutSeconds, enabled }) => ({
    id,
    on: [...on],
    run,
    ...(project ? { project } : {}),
    timeoutSeconds,
    enabled,
  }))

onMounted(() => void settings.loadHooks())

watch(
  () => settings.hooks,
  (hooks) => {
    if (!dirty.value) draft.value = toConfig(hooks)
  },
  { immediate: true },
)

const lastRuns = computed(() => new Map(settings.hooks.map((h) => [h.id, h.lastRun])))

function change(index: number, hook: HookConfig) {
  draft.value = draft.value.map((h, i) => (i === index ? hook : h))
  dirty.value = true
}

function add() {
  draft.value = [
    ...draft.value,
    { id: '', on: ['task.done'], run: '', timeoutSeconds: 20, enabled: true },
  ]
  dirty.value = true
}

function remove(index: number) {
  draft.value = draft.value.filter((_, i) => i !== index)
  dirty.value = true
}

function discard() {
  dirty.value = false
  errors.value = {}
  failure.value = ''
  draft.value = toConfig(settings.hooks)
}

async function save() {
  saving.value = true
  errors.value = {}
  failure.value = ''
  try {
    await settings.saveHooks(draft.value)
    dirty.value = false
    draft.value = toConfig(settings.hooks)
    toast.show({ message: t('settings.hooks.saved'), tone: 'success' })
  } catch (error) {
    const problems = fieldProblems(error)
    errors.value = Object.fromEntries(problems.map((p) => [p.field, p.message]))
    failure.value = problems.length ? t('settings.hooks.invalid') : errorMessage(error)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <SettingRow item="webhooks" stacked :highlighted="highlight === 'webhooks'">
    <div class="hooks">
      <Spinner v-if="settings.hooksStatus === 'loading'" :label="t('settings.hooks.loading')" />
      <EmptyState
        v-else-if="settings.hooksUnsupported"
        icon="webhook"
        :title="t('settings.comingSoon.title')"
        :description="t('settings.hooks.unsupported')"
        :level="3"
      />
      <div v-else-if="settings.hooksStatus === 'error'" class="hooks__note" role="alert">
        {{ t('settings.hooks.loadFailed', { error: settings.hooksError }) }}
        <Button size="sm" variant="ghost" @click="settings.loadHooks()">
          {{ t('common.action.retry') }}
        </Button>
      </div>
      <template v-else>
        <p v-if="!draft.length" class="hooks__note">{{ t('settings.hooks.empty') }}</p>
        <ul class="hooks__list">
          <HookCard
            v-for="(hook, index) in draft"
            :key="hook.id || `new-${index}`"
            :model-value="hook"
            :index="index"
            :last-run="lastRuns.get(hook.id) ?? null"
            :errors="errors"
            @update:model-value="change(index, $event)"
            @remove="remove(index)"
          />
        </ul>
        <p v-if="failure" class="hooks__error" role="alert">{{ failure }}</p>
        <div class="hooks__actions">
          <Button size="sm" variant="secondary" icon="plus" @click="add">
            {{ t('settings.hooks.add') }}
          </Button>
          <span class="hooks__spacer" />
          <Button v-if="dirty" size="sm" variant="ghost" :disabled="saving" @click="discard">
            {{ t('settings.hooks.discard') }}
          </Button>
          <Button size="sm" variant="primary" :disabled="!dirty" :loading="saving" @click="save">
            {{ t('settings.hooks.save') }}
          </Button>
        </div>
      </template>
    </div>
  </SettingRow>
</template>

<style scoped>
.hooks {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  margin-top: var(--space-2);
}

.hooks__list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.hooks__note {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.hooks__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
}

.hooks__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.hooks__spacer {
  flex: 1;
}
</style>
