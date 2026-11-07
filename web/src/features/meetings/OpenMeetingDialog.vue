<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import { errorMessage } from '@/api/client'
import type { MeetingRequest } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Checkbox from '@/components/ui/Checkbox.vue'
import Dialog from '@/components/ui/Dialog.vue'
import NumberField from '@/components/ui/NumberField.vue'
import TextArea from '@/components/ui/TextArea.vue'
import TextField from '@/components/ui/TextField.vue'
import { useConnectionStore } from '@/stores/connection'
import { useTeamStore } from '@/stores/team'
import { useToastStore } from '@/stores/toast'

import { lines } from './meetingModel'

/**
 * Opens a meeting from the dashboard: the question, the project folder, one to six attendees, optional options,
 * files to read first, the number of rounds and a sealed position. Estimate asks the daemon how many runs it
 * takes at most before anything starts.
 */
const open = defineModel<boolean>('open', { required: true })

const { t } = useI18n()
const router = useRouter()
const team = useTeamStore()
const connection = useConnectionStore()
const toast = useToastStore()

const question = ref('')
const title = ref('')
const cwd = ref('')
const attendees = ref<string[]>([])
const options = ref('')
const context = ref('')
const rounds = ref(3)
const sealed = ref('')
const estimate = ref('')
const busy = ref(false)
const errors = ref<Record<string, string>>({})
const failure = ref('')

const usable = computed(() => team.members.filter((m) => m.enabled && !m.invalid))
/** The ticked members that are still listed: one turned off or removed meanwhile is never sent. */
const chosen = computed(() => {
  const ids = new Set(usable.value.map((m) => m.id))
  return attendees.value.filter((a) => ids.has(a))
})

// An estimate answers one exact request: any change makes it stale, also one made while it was asked.
let edits = 0
watch([question, title, cwd, chosen, options, context, rounds, sealed], () => {
  edits++
  estimate.value = ''
})

watch(open, (now) => {
  if (!now) return
  team.ensureMembers()
  if (!cwd.value) cwd.value = connection.projects[0]?.cwd ?? ''
  estimate.value = ''
  failure.value = ''
  errors.value = {}
})

function toggle(id: string, on: boolean) {
  attendees.value = on ? [...attendees.value, id] : attendees.value.filter((a) => a !== id)
}

function request(): MeetingRequest | null {
  const next: Record<string, string> = {}
  if (!question.value.trim()) next.question = t('team.meetings.open.errors.question')
  if (!/^([a-zA-Z]:[\\/]|[\\/])/.test(cwd.value.trim()))
    next.cwd = t('team.meetings.open.errors.cwd')
  if (chosen.value.length < 1 || chosen.value.length > 6) {
    next.attendees = t('team.meetings.open.errors.attendees')
  }
  errors.value = next
  if (Object.keys(next).length) return null
  const opts = lines(options.value)
  const files = lines(context.value)
  return {
    question: question.value.trim(),
    cwd: cwd.value.trim(),
    attendees: [...chosen.value],
    maxRounds: rounds.value,
    ...(title.value.trim() ? { title: title.value.trim() } : {}),
    ...(opts.length ? { options: opts } : {}),
    ...(files.length ? { context: files } : {}),
    ...(sealed.value.trim() ? { sealed: sealed.value.trim() } : {}),
  }
}

async function runEstimate() {
  const req = request()
  if (!req) return
  busy.value = true
  failure.value = ''
  const asked = edits
  try {
    const e = await team.api().estimateMeeting(req)
    if (asked === edits) estimate.value = t('team.meetings.open.estimateResult', { runs: e.runs })
  } catch (error) {
    failure.value = errorMessage(error)
  } finally {
    busy.value = false
  }
}

async function submit() {
  const req = request()
  if (!req || busy.value) return
  busy.value = true
  failure.value = ''
  try {
    const m = await team.api().openMeeting(req)
    toast.show({ message: t('team.meetings.open.opened'), tone: 'success' })
    open.value = false
    question.value = ''
    title.value = ''
    options.value = ''
    context.value = ''
    sealed.value = ''
    attendees.value = []
    void router.push({ name: 'meetings', params: { meetingId: m.id } })
  } catch (error) {
    failure.value = errorMessage(error)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog
    :open="open"
    size="lg"
    :title="t('team.meetings.open.title')"
    :dismissible="!busy"
    initial-focus="textarea"
    @update:open="open = $event"
  >
    <form id="open-meeting" class="form" @submit.prevent="submit">
      <TextArea
        v-model="question"
        :label="t('team.meetings.open.question')"
        :hint="t('team.meetings.open.questionHint')"
        :error="errors.question"
        :rows="3"
      />
      <div class="form__row">
        <TextField v-model="title" :label="t('team.meetings.open.titleField')" />
        <TextField
          v-model="cwd"
          :label="t('team.meetings.open.cwd')"
          :hint="t('team.meetings.open.cwdHint')"
          :error="errors.cwd"
        />
      </div>
      <fieldset class="form__set">
        <legend class="form__legend">{{ t('team.meetings.open.attendees') }}</legend>
        <p class="form__hint">{{ t('team.meetings.open.attendeesHint') }}</p>
        <p v-if="!usable.length" class="form__hint">{{ t('team.meetings.open.noMembers') }}</p>
        <div class="form__checks">
          <Checkbox
            v-for="m in usable"
            :key="m.id"
            :model-value="attendees.includes(m.id)"
            :label="m.name"
            :description="t(`team.rank.${m.rank}`)"
            @update:model-value="toggle(m.id, $event)"
          />
        </div>
        <p v-if="errors.attendees" class="form__error">{{ errors.attendees }}</p>
      </fieldset>
      <TextArea
        v-model="options"
        :label="t('team.meetings.open.options')"
        :hint="t('team.meetings.open.optionsHint')"
        :rows="3"
      />
      <TextArea v-model="context" :label="t('team.meetings.open.context')" :rows="2" mono />
      <div class="form__row">
        <NumberField
          :model-value="rounds"
          :label="t('team.meetings.open.rounds')"
          integer
          @commit="rounds = Math.min(5, Math.max(1, $event))"
        />
      </div>
      <TextArea
        v-model="sealed"
        :label="t('team.meetings.open.sealed')"
        :hint="t('team.meetings.open.sealedHint')"
        :rows="2"
      />
      <p v-if="estimate" class="form__estimate tone-neutral" role="status">{{ estimate }}</p>
      <p v-if="failure" class="form__error" role="alert">{{ failure }}</p>
    </form>
    <template #footer>
      <Button variant="ghost" :disabled="busy" @click="runEstimate">
        {{ t('team.meetings.open.estimate') }}
      </Button>
      <Button variant="ghost" :disabled="busy" @click="open = false">
        {{ t('common.action.cancel') }}
      </Button>
      <Button type="submit" form="open-meeting" variant="primary" :loading="busy">
        {{ t('team.meetings.open.submit') }}
      </Button>
    </template>
  </Dialog>
</template>

<style scoped>
.form {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.form__row {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 220px), 1fr));
  align-items: start;
  gap: var(--space-3);
}

.form__set {
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}

.form__legend {
  padding: 0;
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.form__hint {
  margin-bottom: var(--space-2);
  color: var(--text-muted);
  font-size: var(--fs-xs);
}

.form__checks {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 180px), 1fr));
  gap: var(--space-2);
}

.form__estimate {
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm);
  background: var(--tone-bg);
  color: var(--tone-fg);
  font-size: var(--fs-sm);
}

.form__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
}
</style>
