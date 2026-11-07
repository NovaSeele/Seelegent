<script setup lang="ts">
import { computed, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import type { Automation } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Dialog from '@/components/ui/Dialog.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useAutomationsStore } from '@/stores/automations'
import { useToastStore } from '@/stores/toast'

import { useScheduleText } from './useScheduleText'

/**
 * The one place an automation is turned on. It reads the automation fresh and shows everything a run does: the
 * prompt in full, the folder, the worker, the gate, the lane, the precheck and the schedule with its next three
 * runs (from the daemon, in the automation's zone and the viewer's own time). Turning on spends a
 * one-time token for exactly this revision; the daemon refuses it if the automation changed in between, and the
 * dialog then shows the new content to confirm again. Only the dashboard can make these two calls.
 */
const props = defineProps<{ automationId: string | null }>()
const open = defineModel<boolean>('open', { required: true })

const { t } = useI18n()
const words = useScheduleText()
const store = useAutomationsStore()
const toast = useToastStore()

const shown = shallowRef<Automation | null>(null)
const loading = shallowRef(false)
const busy = shallowRef(false)
const error = shallowRef('')
// The next runs, or null while unknown (a preview that fails leaves the schedule in words alone).
const runs = shallowRef<string[] | null>(null)

async function nextRuns(a: Automation): Promise<void> {
  try {
    const res = await store.api().preview({ schedule: a.schedule, timezone: a.timezone, count: 3 })
    if (shown.value === a) runs.value = res.runs
  } catch {
    runs.value = null
  }
}

async function read(): Promise<void> {
  const id = props.automationId
  if (!id) return
  loading.value = true
  runs.value = null
  try {
    const a = await store.api().get(id)
    shown.value = a
    void nextRuns(a)
  } catch (cause) {
    shown.value = null
    error.value = errorMessage(cause)
  } finally {
    loading.value = false
  }
}

// immediate: the dialog can be open from the start (the ?confirm= link of the lead's message).
watch(
  [open, () => props.automationId],
  ([now]) => {
    if (!now) return
    shown.value = null
    error.value = ''
    void read()
  },
  { immediate: true },
)

const schedule = computed(() => (shown.value ? words.schedule(shown.value.schedule) : ''))
const worker = computed(() => {
  const a = shown.value
  if (!a) return ''
  const who = a.member || a.backend || t('automations.confirm.defaultWorker')
  return a.model ? `${who} · ${a.model}` : who
})
const gate = computed(() => {
  const a = shown.value
  if (!a) return ''
  const word = t(`automations.editor.gate.${a.gate === null ? 'default' : a.gate ? 'on' : 'off'}`)
  return a.gateCommands.length ? `${word}: ${a.gateCommands.join('; ')}` : word
})

async function turnOn() {
  const a = shown.value
  if (!a || busy.value) return
  busy.value = true
  error.value = ''
  try {
    if (!a.enableRequested) await store.requestEnable(a.id)
    const on = await store.confirm(a)
    toast.show({ message: t('automations.confirm.done', { name: on.name }), tone: 'success' })
    open.value = false
  } catch (cause) {
    error.value = errorMessage(cause)
    await read()
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog
    :open="open"
    size="lg"
    :title="t('automations.confirm.title')"
    :description="t('automations.confirm.desc')"
    :dismissible="!busy"
    @update:open="open = $event"
  >
    <Spinner v-if="loading && !shown" :label="t('automations.confirm.loading')" />
    <div v-else-if="shown" class="conf">
      <p v-if="shown.enabled" class="conf__note" role="status">
        {{ t('automations.confirm.already') }}
      </p>
      <dl class="conf__facts">
        <dt>{{ t('automations.editor.name') }}</dt>
        <dd>{{ shown.name }}</dd>
        <dt>{{ t('automations.editor.cwd') }}</dt>
        <dd>
          <code>{{ shown.cwd }}</code>
        </dd>
        <dt>{{ t('automations.editor.schedule') }}</dt>
        <dd>
          {{ schedule }} ({{ shown.timezone }})
          <template v-if="shown.schedule.cron">
            · <code>{{ shown.schedule.cron }}</code>
          </template>
          <span v-if="runs && !runs.length" class="conf__never">{{
            t('automations.cronError.never')
          }}</span>
          <span v-else-if="runs" class="conf__runs">
            {{ t('automations.confirm.nextRuns') }}
            <ul>
              <li v-for="r in runs" :key="r">{{ words.runTime(r, shown.timezone) }}</li>
            </ul>
          </span>
        </dd>
        <dt>{{ t('automations.confirm.worker') }}</dt>
        <dd>{{ worker }}</dd>
        <dt>{{ t('automations.editor.gateLabel') }}</dt>
        <dd>{{ gate }}</dd>
        <dt>{{ t('automations.editor.lane') }}</dt>
        <dd>
          <template v-if="shown.noLane">{{ t('automations.editor.noLane') }}</template>
          <code v-else-if="shown.lane.length">{{ shown.lane.join(', ') }}</code>
          <template v-else>{{ t('automations.confirm.laneDefault') }}</template>
        </dd>
        <dt>{{ t('automations.editor.precheck') }}</dt>
        <dd>
          <code v-if="shown.precheck">{{ shown.precheck }}</code>
          <template v-else>{{ t('automations.confirm.none') }}</template>
        </dd>
        <dt>{{ t('automations.confirm.quota') }}</dt>
        <dd>
          {{ t('automations.confirm.quotaText', { n: shown.quota.minRemaining }) }}
          <template v-if="shown.quota.useBeforeReset"
            >· {{ t('automations.editor.beforeReset') }}</template
          >
        </dd>
        <dt>{{ t('automations.editor.grace') }}</dt>
        <dd>{{ t('automations.confirm.graceText', { n: shown.graceMinutes }) }}</dd>
      </dl>
      <h3 class="conf__h">{{ t('automations.editor.prompt') }}</h3>
      <pre class="conf__prompt">{{ shown.prompt }}</pre>
      <p class="conf__warn">{{ t('automations.confirm.warn') }}</p>
    </div>
    <p v-if="error" class="conf__error" role="alert">{{ error }}</p>
    <template #footer>
      <Button variant="ghost" :disabled="busy" @click="open = false">
        {{ t('common.action.cancel') }}
      </Button>
      <Button
        variant="primary"
        :loading="busy"
        :disabled="!shown || shown.enabled || loading"
        @click="turnOn"
      >
        {{ t('automations.confirm.submit') }}
      </Button>
    </template>
  </Dialog>
</template>

<style scoped>
.conf {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.conf__facts {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: var(--space-1) var(--space-3);
  margin: 0;
  font-size: var(--fs-sm);
}

.conf__facts dt {
  color: var(--text-secondary);
}

.conf__facts dd {
  min-width: 0;
  margin: 0;
  overflow-wrap: anywhere;
}

@media (width < 600px) {
  .conf__facts {
    grid-template-columns: 1fr;
  }

  .conf__facts dd {
    margin-bottom: var(--space-2);
  }
}

.conf__runs,
.conf__never {
  display: block;
  margin-top: var(--space-1);
  color: var(--text-secondary);
}

.conf__never {
  color: var(--attention-fg);
}

.conf__runs ul {
  margin: 0;
  padding-left: var(--space-4);
}

.conf__h {
  font-size: var(--fs-sm);
  font-weight: var(--fw-semibold);
}

.conf__prompt {
  max-height: 280px;
  margin: 0;
  padding: var(--space-3);
  overflow: auto;
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-surface-2);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.conf__note,
.conf__warn {
  color: var(--attention-fg);
  font-size: var(--fs-sm);
}

.conf__error {
  margin-top: var(--space-3);
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
  overflow-wrap: anywhere;
}
</style>
