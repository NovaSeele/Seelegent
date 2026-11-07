<script setup lang="ts">
import { computed, onBeforeUnmount, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import type { SchedulePreview } from '@/api/types'
import SegmentedControl, { type SegmentOption } from '@/components/ui/SegmentedControl.vue'
import Select, { type SelectOption } from '@/components/ui/Select.vue'
import TextField from '@/components/ui/TextField.vue'
import { useAutomationsStore } from '@/stores/automations'

import {
  EVERY,
  formSchedule,
  type AutomationForm,
  type Every,
  type FormProblem,
  type ScheduleMode,
  weekdayName,
} from './automationModel'
import { useFormFields } from './useFormFields'

/**
 * When an automation runs: a preset (hourly, daily, weekdays, weekly at a time) or a 5-field cron expression, in
 * a time zone. The next run times come from the daemon (the same code that runs the schedule), a moment after
 * the last change.
 */
const form = defineModel<AutomationForm>({ required: true })
defineProps<{ problems: readonly FormProblem[] }>()

const { t, locale } = useI18n()
const store = useAutomationsStore()
const field = useFormFields(form)
const mode = field('mode')
const every = field('every')
const at = field('at')
const weekday = field('weekday')
const cron = field('cron')
const timezone = field('timezone')

const modes = computed<SegmentOption<ScheduleMode>[]>(() => [
  { value: 'preset', label: t('automations.editor.preset') },
  { value: 'cron', label: t('automations.editor.cron') },
])
const everyOptions = computed<SelectOption<Every>[]>(() =>
  EVERY.map((value) => ({ value, label: t(`automations.editor.every.${value}`) })),
)
const weekdays = computed<SelectOption<number>[]>(() =>
  [1, 2, 3, 4, 5, 6, 0].map((value) => ({ value, label: weekdayName(value, locale.value) })),
)

const preview = shallowRef<SchedulePreview | null>(null)
const previewError = shallowRef('')
let timer: ReturnType<typeof setTimeout> | undefined
let abort: AbortController | undefined

function refresh() {
  clearTimeout(timer)
  timer = setTimeout(async () => {
    abort?.abort()
    const ctl = new AbortController()
    abort = ctl
    try {
      const res = await store.api().preview(
        {
          schedule: formSchedule(form.value),
          timezone: form.value.timezone.trim() || 'local',
          count: 5,
        },
        ctl.signal,
      )
      if (abort !== ctl) return
      preview.value = res
      previewError.value = ''
    } catch (cause) {
      if (ctl.signal.aborted) return
      preview.value = null
      previewError.value = errorMessage(cause)
    }
  }, 400)
}

watch(
  () => [mode.value, every.value, at.value, weekday.value, cron.value, timezone.value],
  refresh,
  { immediate: true },
)
onBeforeUnmount(() => {
  clearTimeout(timer)
  abort?.abort()
})
</script>

<template>
  <fieldset class="sched">
    <legend class="sched__legend">{{ t('automations.editor.schedule') }}</legend>
    <SegmentedControl
      v-model="mode"
      :options="modes"
      :label="t('automations.editor.schedule')"
      size="sm"
    />
    <div class="sched__row">
      <template v-if="mode === 'preset'">
        <Select
          v-model="every"
          :label="t('automations.editor.everyLabel')"
          :options="everyOptions"
        />
        <Select
          v-if="every === 'weekly'"
          v-model="weekday"
          :label="t('automations.editor.weekday')"
          :options="weekdays"
        />
        <TextField
          v-model="at"
          :label="
            every === 'hourly' ? t('automations.editor.atHourly') : t('automations.editor.at')
          "
          :hint="t('automations.editor.atHint')"
          :error="problems.includes('at') ? t('automations.editor.atError') : undefined"
        />
      </template>
      <TextField
        v-else
        v-model="cron"
        class="sched__cron"
        :label="t('automations.editor.cronLabel')"
        :hint="t('automations.editor.cronHint')"
        :error="problems.includes('cron') ? t('automations.editor.cronError') : undefined"
      />
      <TextField
        v-model="timezone"
        :label="t('automations.editor.timezone')"
        :hint="t('automations.editor.timezoneHint')"
      />
    </div>
    <div class="sched__preview" aria-live="polite">
      <p v-if="previewError" class="sched__error">{{ previewError }}</p>
      <template v-else-if="preview">
        <p class="sched__label">
          {{ t('automations.editor.nextRuns', { zone: preview.timezone }) }}
        </p>
        <ul class="sched__runs">
          <li v-for="r in preview.local" :key="r">{{ r }}</li>
        </ul>
      </template>
    </div>
  </fieldset>
</template>

<style scoped>
.sched {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}

.sched__legend {
  padding: 0;
  margin-bottom: var(--space-2);
  font-size: var(--fs-sm);
  font-weight: var(--fw-semibold);
}

.sched__row {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 160px), 1fr));
  align-items: start;
  gap: var(--space-3);
}

.sched__cron {
  grid-column: span 2;
}

@media (width < 600px) {
  .sched__cron {
    grid-column: auto;
  }
}

.sched__preview {
  font-size: var(--fs-xs);
}

.sched__label {
  color: var(--text-secondary);
}

.sched__runs {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-3);
  margin: var(--space-1) 0 0;
  padding: 0;
  list-style: none;
  font-family: var(--font-mono);
}

.sched__error {
  color: var(--status-danger-fg);
  overflow-wrap: anywhere;
}
</style>
