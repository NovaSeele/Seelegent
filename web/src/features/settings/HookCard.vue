<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import { ApiError, errorMessage } from '@/api/client'
import type { HookConfig, HookRun } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Chip from '@/components/ui/Chip.vue'
import NumberField from '@/components/ui/NumberField.vue'
import Switch from '@/components/ui/Switch.vue'
import TextField from '@/components/ui/TextField.vue'
import { useFormat } from '@/composables/useFormat'
import { useSettingsStore } from '@/stores/settings'

import { HOOK_EVENTS } from './hookEvents'

/**
 * One hook in the list being edited: its command, events, optional project folder, timeout and on/off, its last
 * run, and a Test button. Testing runs the saved hook by id, so a hook not saved yet cannot be tested; a daemon
 * that cannot test hooks yet answers 501, which is said here instead of hiding the button.
 */
const props = defineProps<{
  index: number
  lastRun: HookRun | null
  errors: Record<string, string>
}>()
const hook = defineModel<HookConfig>({ required: true })
const emit = defineEmits<{ remove: [] }>()

const { t } = useI18n()
const fmt = useFormat()
const settings = useSettingsStore()
const id = useId()
const testing = ref(false)
const testResult = ref('')

const err = (field: string) => props.errors[`hooks[${props.index}].${field}`]

function update(patch: Partial<HookConfig>) {
  hook.value = { ...hook.value, ...patch }
}

function toggleEvent(event: string) {
  const on = hook.value.on.includes(event)
  update({ on: on ? hook.value.on.filter((e) => e !== event) : [...hook.value.on, event] })
}

const lastRunText = computed(() => {
  const run = props.lastRun
  if (!run) return t('settings.hooks.neverRan')
  const outcome = run.timedOut
    ? t('settings.hooks.timedOut')
    : t('settings.hooks.exit', { code: run.exitCode ?? '?' })
  return t('settings.hooks.lastRun', {
    time: fmt.value.dateTime(run.at),
    event: run.event,
    outcome,
  })
})

async function test() {
  testing.value = true
  testResult.value = ''
  try {
    const run = await settings.testHook(hook.value.id)
    testResult.value = run.timedOut
      ? t('settings.hooks.timedOut')
      : t('settings.hooks.exit', { code: run.exitCode ?? '?' })
  } catch (error) {
    testResult.value =
      error instanceof ApiError && error.status === 501
        ? t('settings.hooks.testUnsupported')
        : t('settings.hooks.testFailed', { error: errorMessage(error) })
  } finally {
    testing.value = false
  }
}
</script>

<template>
  <li class="hook" :aria-labelledby="`${id}-title`">
    <div class="hook__head">
      <h4 :id="`${id}-title`" class="hook__title">
        {{ t('settings.hooks.hookN', { n: index + 1 }) }}
      </h4>
      <Switch
        :model-value="hook.enabled"
        :label="t('settings.hooks.enabled')"
        @update:model-value="update({ enabled: $event })"
      />
    </div>
    <TextField
      :model-value="hook.run"
      :label="t('settings.hooks.run')"
      :hint="t('settings.hooks.runHint')"
      :error="err('run')"
      size="sm"
      @update:model-value="update({ run: $event })"
    />
    <fieldset class="hook__events">
      <legend class="hook__legend">{{ t('settings.hooks.on') }}</legend>
      <div class="hook__chips">
        <Chip
          v-for="event in HOOK_EVENTS"
          :key="event"
          :selected="hook.on.includes(event)"
          @toggle="toggleEvent(event)"
        >
          {{ event }}
        </Chip>
      </div>
      <p v-if="err('on')" class="hook__error">{{ err('on') }}</p>
    </fieldset>
    <div class="hook__row">
      <TextField
        :model-value="hook.project ?? ''"
        :label="t('settings.hooks.project')"
        :hint="t('settings.hooks.projectHint')"
        size="sm"
        @update:model-value="update({ project: $event || undefined })"
      />
      <NumberField
        :model-value="hook.timeoutSeconds"
        :label="t('settings.hooks.timeout')"
        :unit="t('settings.units.s')"
        :error="err('timeoutSeconds')"
        size="sm"
        @commit="update({ timeoutSeconds: Math.round($event) })"
      />
    </div>
    <div class="hook__foot">
      <p class="hook__last">{{ hook.id ? lastRunText : t('settings.hooks.unsaved') }}</p>
      <Button size="sm" variant="secondary" :loading="testing" :disabled="!hook.id" @click="test">
        {{ t('settings.hooks.test') }}
      </Button>
      <Button size="sm" variant="ghost" @click="emit('remove')">
        {{ t('settings.hooks.remove') }}
      </Button>
    </div>
    <p v-if="testResult" class="hook__result tone-neutral" role="status">{{ testResult }}</p>
  </li>
</template>

<style scoped>
.hook {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
}

.hook__head,
.hook__foot {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.hook__title {
  font-size: var(--fs-sm);
  font-weight: var(--fw-semibold);
}

.hook__events {
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}

.hook__legend {
  margin-bottom: var(--space-1);
  padding: 0;
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.hook__chips {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.hook__row {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 200px), 1fr));
  gap: var(--space-2);
}

.hook__last {
  flex: 1;
  color: var(--text-muted);
  font-size: var(--fs-xs);
}

.hook__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-xs);
}

.hook__result {
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm);
  background: var(--tone-bg);
  color: var(--tone-fg);
  font-size: var(--fs-xs);
}
</style>
