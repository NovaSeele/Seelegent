<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import type { Meeting } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import IconButton from '@/components/ui/IconButton.vue'
import Select from '@/components/ui/Select.vue'
import TextArea from '@/components/ui/TextArea.vue'
import { useTeamStore } from '@/stores/team'
import { useToastStore } from '@/stores/toast'

import { attendeeName, pendingQuestions } from './meetingModel'

/**
 * What the lead does in a meeting from the dashboard: answer the attendees' questions (waiting_lead), record the
 * decision (deciding), and turn a closed meeting's decision into delegated tasks. Each answer replaces the meeting.
 */
const props = defineProps<{ meeting: Meeting }>()
const emit = defineEmits<{ updated: [meeting: Meeting] }>()

const { t } = useI18n()
const team = useTeamStore()
const toast = useToastStore()

const answers = ref<Record<string, string>>({})
const note = ref('')
const decision = ref('')
const rationale = ref('')
const option = ref('')
const items = ref<string[]>([''])
const busy = ref(false)
const failure = ref('')

const pending = computed(() => pendingQuestions(props.meeting))
const deciding = computed(() => props.meeting.status === 'deciding')
const acting = computed(() => props.meeting.status === 'closed' && !!props.meeting.decision)
const optionChoices = computed(() => [
  { value: '', label: t('team.meetings.decide.noOption') },
  ...props.meeting.options.map((o) => ({ value: o.key, label: `${o.key}. ${o.text}` })),
])
const filled = computed(() => items.value.map((i) => i.trim()).filter(Boolean))

watch(
  () => props.meeting.status,
  () => (failure.value = ''),
)

async function run(action: () => Promise<Meeting>, done: string) {
  busy.value = true
  failure.value = ''
  try {
    emit('updated', await action())
    toast.show({ message: done, tone: 'success' })
    return true
  } catch (error) {
    failure.value = errorMessage(error)
    return false
  } finally {
    busy.value = false
  }
}

async function post() {
  const list = pending.value
    .map((q) => ({ id: q.id, answer: (answers.value[q.id] ?? '').trim() }))
    .filter((a) => a.answer)
  const ok = await run(
    () =>
      team.api().postMeeting(props.meeting.id, {
        answers: list,
        ...(note.value.trim() ? { note: note.value.trim() } : {}),
      }),
    t('team.meetings.lead.posted'),
  )
  if (ok) {
    answers.value = {}
    note.value = ''
  }
}

async function decide() {
  await run(
    () =>
      team.api().decideMeeting(props.meeting.id, {
        decision: decision.value.trim(),
        ...(rationale.value.trim() ? { rationale: rationale.value.trim() } : {}),
        ...(option.value ? { option: option.value } : {}),
      }),
    t('team.meetings.decide.done'),
  )
}

async function act() {
  const n = filled.value.length
  const ok = await run(
    async () => {
      const res = await team.api().actMeeting(props.meeting.id, {
        items: filled.value.map((text) => ({ text })),
      })
      return res.meeting
    },
    t('team.meetings.act.done', { n }, n),
  )
  if (ok) items.value = ['']
}
</script>

<template>
  <section v-if="pending.length || deciding || acting" class="lead">
    <form v-if="pending.length" class="lead__form" @submit.prevent="post">
      <h2 class="lead__title">{{ t('team.meetings.lead.title') }}</h2>
      <div v-for="q in pending" :key="q.id" class="lead__q">
        <p class="lead__question">
          <strong>{{ attendeeName(meeting, q.label) }}</strong>
          {{ q.question }}
        </p>
        <TextArea
          v-model="answers[q.id]"
          :label="t('team.meetings.lead.answer', { label: q.label, id: q.id })"
          :rows="2"
        />
      </div>
      <TextArea v-model="note" :label="t('team.meetings.lead.note')" :rows="2" />
      <Button
        type="submit"
        variant="primary"
        :loading="busy"
        :disabled="!Object.values(answers).some((a) => a?.trim())"
      >
        {{ t('team.meetings.lead.post') }}
      </Button>
    </form>

    <form v-else-if="deciding" class="lead__form" @submit.prevent="decide">
      <h2 class="lead__title">{{ t('team.meetings.decide.title') }}</h2>
      <Select
        v-if="meeting.options.length"
        v-model="option"
        :label="t('team.meetings.decide.option')"
        :options="optionChoices"
      />
      <TextArea v-model="decision" :label="t('team.meetings.decide.decision')" :rows="3" />
      <TextArea v-model="rationale" :label="t('team.meetings.decide.rationale')" :rows="2" />
      <Button type="submit" variant="primary" :loading="busy" :disabled="!decision.trim()">
        {{ t('team.meetings.decide.submit') }}
      </Button>
    </form>

    <form v-else class="lead__form" @submit.prevent="act">
      <h2 class="lead__title">{{ t('team.meetings.act.title') }}</h2>
      <div v-for="(_, i) in items" :key="i" class="lead__item">
        <TextArea v-model="items[i]" :label="t('team.meetings.act.item', { n: i + 1 })" :rows="2" />
        <IconButton
          v-if="items.length > 1"
          icon="trash"
          size="sm"
          :label="t('team.meetings.act.remove', { n: i + 1 })"
          @click="items.splice(i, 1)"
        />
      </div>
      <div class="lead__actions">
        <Button size="sm" variant="secondary" icon="plus" @click="items.push('')">
          {{ t('team.meetings.act.add') }}
        </Button>
        <Button type="submit" variant="primary" :loading="busy" :disabled="!filled.length">
          {{ t('team.meetings.act.submit', { n: filled.length }, filled.length) }}
        </Button>
      </div>
    </form>
    <p v-if="failure" class="lead__error" role="alert">{{ failure }}</p>
  </section>
</template>

<style scoped>
.lead {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-4);
  border: var(--border-width) solid var(--attention-border);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
}

.lead__form {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: var(--space-3);
}

.lead__form > :deep(.btn) {
  align-self: flex-start;
}

.lead__title {
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
}

.lead__q {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.lead__question {
  font-size: var(--fs-sm);
  overflow-wrap: anywhere;
}

.lead__item {
  display: flex;
  align-items: flex-end;
  gap: var(--space-2);
}

.lead__item > :first-child {
  flex: 1;
}

.lead__actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.lead__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
}
</style>
