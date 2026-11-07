<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import type { Dissent, Meeting } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import StatusPill from '@/components/ui/StatusPill.vue'
import { useTeamStore } from '@/stores/team'

import ClaimList from './ClaimList.vue'
import MeetingActForm from './MeetingActForm.vue'
import { attendeeName } from './meetingModel'

/**
 * What a meeting came to: the decision, each peer's dissent or concession (with the files it watches, the later
 * tasks that went wrong on them, and the lead's vindicate or dismiss), the advice not taken and the tasks created.
 */
const props = defineProps<{ meeting: Meeting }>()
const emit = defineEmits<{ updated: [meeting: Meeting] }>()

const { t } = useI18n()
const team = useTeamStore()
const busy = ref<string | null>(null)
const failure = ref('')
/** A closed meeting's decision can still become tasks: the form folds inside the Tasks block. */
const acting = computed(() => props.meeting.status === 'closed' && !!props.meeting.decision)

async function outcome(d: Dissent, value: 'vindicated' | 'dismissed') {
  busy.value = d.id
  failure.value = ''
  try {
    emit('updated', await team.api().dissentOutcome(props.meeting.id, d.id, value))
  } catch (error) {
    failure.value = errorMessage(error)
  } finally {
    busy.value = null
  }
}
</script>

<template>
  <section v-if="meeting.decision" class="block">
    <h2 class="block__title">{{ t('team.meetings.decision') }}</h2>
    <p class="block__text">
      <strong v-if="meeting.decision.option">{{
        t('team.meetings.optionLead', { key: meeting.decision.option })
      }}</strong>
      {{ meeting.decision.text }}
    </p>
    <p v-if="meeting.decision.rationale" class="block__muted">{{ meeting.decision.rationale }}</p>
  </section>

  <section v-if="meeting.dissents.length" class="block">
    <h2 class="block__title">{{ t('team.meetings.dissents') }}</h2>
    <article v-for="d in meeting.dissents" :key="d.id" class="dissent">
      <header class="dissent__head">
        <strong>{{ attendeeName(meeting, d.label) }}</strong>
        <StatusPill
          size="sm"
          :tone="d.kind === 'absent' ? 'muted' : 'neutral'"
          :label="t(`team.meetings.kind.${d.kind}`)"
        />
        <!-- Amber only when later work went wrong on the files it watches: then the lead has to look. -->
        <StatusPill
          v-if="d.signals.length && !d.outcome"
          size="sm"
          tone="attention"
          icon="warning"
          :label="t('team.meetings.signalsN', { n: d.signals.length }, d.signals.length)"
        />
        <StatusPill
          v-if="d.outcome"
          size="sm"
          :tone="d.outcome === 'vindicated' ? 'success' : 'muted'"
          :label="t(`team.meetings.outcome.${d.outcome}`)"
        />
      </header>
      <p class="block__text">{{ d.text }}</p>
      <ClaimList v-if="d.claims.length" :claims="d.claims" />
      <p v-if="d.watchPaths.length" class="block__muted">
        {{ t('team.meetings.watchingLead') }}
        <code v-for="p in d.watchPaths" :key="p" class="dissent__path">{{ p }}</code>
      </p>
      <ul v-if="d.signals.length" class="dissent__signals">
        <li v-for="s in d.signals" :key="s.taskId + s.reason">
          <RouterLink :to="{ name: 'tasks', params: { taskId: s.taskId } }">
            {{ t(`team.meetings.signal.${s.reason}`, { task: s.taskId }) }}
          </RouterLink>
        </li>
      </ul>
      <div v-if="d.kind === 'dissent' && !d.outcome" class="dissent__actions">
        <Button
          size="sm"
          variant="secondary"
          :loading="busy === d.id"
          @click="outcome(d, 'vindicated')"
        >
          {{ t('team.meetings.vindicate') }}
        </Button>
        <Button
          size="sm"
          variant="ghost"
          :disabled="busy === d.id"
          @click="outcome(d, 'dismissed')"
        >
          {{ t('team.meetings.dismiss') }}
        </Button>
      </div>
    </article>
    <p v-if="failure" class="block__error" role="alert">{{ failure }}</p>
  </section>

  <section v-if="meeting.adviceNotTaken.length" class="block">
    <h2 class="block__title">{{ t('team.meetings.adviceNotTaken') }}</h2>
    <p v-for="a in meeting.adviceNotTaken" :key="a.label" class="block__text">
      <strong>{{ t('team.meetings.speaker', { who: attendeeName(meeting, a.label) }) }}</strong>
      {{ a.stance }}
    </p>
  </section>

  <section v-if="meeting.actions.length || acting" class="block">
    <h2 class="block__title">{{ t('team.meetings.tasks') }}</h2>
    <ul v-if="meeting.actions.length" class="block__list">
      <li v-for="a in meeting.actions" :key="a.taskId" class="task">
        <RouterLink :to="{ name: 'tasks', params: { taskId: a.taskId } }">{{ a.text }}</RouterLink>
        <span class="block__muted task__id">{{ a.taskId }}</span>
      </li>
    </ul>
    <details v-if="acting" class="add" :open="!meeting.actions.length">
      <summary class="add__summary">{{ t('team.meetings.act.open') }}</summary>
      <MeetingActForm :meeting="meeting" @updated="emit('updated', $event)" />
    </details>
  </section>
</template>

<style scoped>
.block {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-4);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
  font-size: var(--fs-sm);
}

.block__title {
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
}

.block__text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.block__muted {
  color: var(--text-secondary);
  overflow-wrap: anywhere;
}

.block__error {
  color: var(--status-danger-fg);
}

.block__list {
  margin: 0;
  padding-left: var(--space-5);
}

.dissent {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding-top: var(--space-2);
  border-top: var(--border-width) solid var(--border-subtle);
}

.dissent__head,
.dissent__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.dissent__path {
  margin-right: var(--space-2);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
}

.dissent__signals {
  margin: 0;
  padding-left: var(--space-5);
}

.task__id {
  margin-left: var(--space-2);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
}

.add {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.add__summary {
  min-height: max(32px, var(--target-min));
  font-weight: var(--fw-medium);
  cursor: pointer;
}

.add__summary:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}

.add[open] > .add__summary {
  margin-bottom: var(--space-2);
}
</style>
