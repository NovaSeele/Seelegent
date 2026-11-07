<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import type { Meeting } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Select from '@/components/ui/Select.vue'
import TextArea from '@/components/ui/TextArea.vue'
import { useTeamStore } from '@/stores/team'
import { useToastStore } from '@/stores/toast'

import { attendeeName, questionGroups } from './meetingModel'

/**
 * What the lead must do now in a meeting from the dashboard: answer the attendees' questions (waiting_lead) and
 * record the decision (deciding). Identical questions are one box whose answer goes to every copy; a note alone
 * may be sent too. Each answer replaces the meeting. Shown, with the attention border, only while the meeting
 * waits for the lead.
 */
const props = defineProps<{ meeting: Meeting }>()
const emit = defineEmits<{ updated: [meeting: Meeting] }>()

const { t } = useI18n()
const team = useTeamStore()
const toast = useToastStore()
const uid = useId()

/** Answers by the group's first question id. */
const answers = ref<Record<string, string>>({})
const note = ref('')
const decision = ref('')
const rationale = ref('')
const option = ref('')
const busy = ref(false)
const failure = ref('')

const groups = computed(() => questionGroups(props.meeting))
const deciding = computed(() => props.meeting.status === 'deciding')
const optionChoices = computed(() => [
  { value: '', label: t('team.meetings.decide.noOption') },
  ...props.meeting.options.map((o) => ({ value: o.key, label: `${o.key}. ${o.text}` })),
])
const answered = computed(
  () => groups.value.filter((g) => (answers.value[g.ids[0] ?? ''] ?? '').trim()).length,
)
const skipped = computed(() => groups.value.length - answered.value)

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
  const list = groups.value.flatMap((g) => {
    const answer = (answers.value[g.ids[0] ?? ''] ?? '').trim()
    return answer ? g.ids.map((id) => ({ id, answer })) : []
  })
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
</script>

<template>
  <section v-if="groups.length || deciding" class="lead">
    <form v-if="groups.length" class="lead__form" @submit.prevent="post">
      <h2 class="lead__title">{{ t('team.meetings.lead.title') }}</h2>
      <div v-for="(g, i) in groups" :key="g.ids[0]" class="lead__q">
        <p :id="`${uid}-q${i}`" class="lead__question">
          <span class="lead__who">{{
            t('team.meetings.lead.askedBy', {
              who: g.labels.map((l) => attendeeName(meeting, l)).join(', '),
            })
          }}</span>
          {{ g.question }}
        </p>
        <TextArea
          v-model="answers[g.ids[0] ?? '']"
          :label="t('team.meetings.lead.answer')"
          :describedby="`${uid}-q${i}`"
          :rows="2"
        />
      </div>
      <TextArea v-model="note" :label="t('team.meetings.lead.note')" :rows="2" />
      <p v-if="answered && skipped" class="lead__skip">
        {{ t('team.meetings.lead.skipped', { n: skipped }, skipped) }}
      </p>
      <Button type="submit" variant="primary" :loading="busy" :disabled="!answered && !note.trim()">
        {{ t('team.meetings.lead.postN', { n: answered, total: groups.length }) }}
      </Button>
    </form>

    <form v-else class="lead__form" @submit.prevent="decide">
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
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  font-size: var(--fs-sm);
  overflow-wrap: anywhere;
}

.lead__who {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.lead__skip {
  color: var(--attention-fg);
  font-size: var(--fs-sm);
}

.lead__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
}
</style>
