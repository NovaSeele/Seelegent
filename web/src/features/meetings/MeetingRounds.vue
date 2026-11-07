<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Meeting, MeetingAnswer } from '@/api/types'
import StatusPill from '@/components/ui/StatusPill.vue'
import type { Tone } from '@/components/ui/tone'

import ClaimList from './ClaimList.vue'
import { attendeeName, leadByRound } from './meetingModel'

/**
 * The rounds of a meeting, newest first: each attendee's answer with its option, stance, confidence, arguments,
 * claims (citations checked) and what it asked the lead. Absent attendees say why.
 */
const props = defineProps<{ meeting: Meeting }>()

const { t } = useI18n()

const lists = ['arguments', 'risks', 'actions', 'questions'] as const
const listOf = (a: MeetingAnswer, key: (typeof lists)[number]) => a[key] ?? []
const who = (label: string) => attendeeName(props.meeting, label)

/** A failed run is a failure (danger); a cancelled or vanished one, or an old record without a reason, is muted. */
const absentTone = (a: MeetingAnswer): Tone => (a.absentReason === 'failed' ? 'danger' : 'muted')
const absentKey = (a: MeetingAnswer) => `team.meetings.absentRun.${a.absentReason ?? 'none'}`

const lead = computed(() => leadByRound(props.meeting))
const leadOf = (n: number) => lead.value.get(n) ?? { answers: [], notes: [] }
</script>

<template>
  <section class="rounds" :aria-label="t('team.meetings.rounds')">
    <h2 class="rounds__title">{{ t('team.meetings.rounds') }}</h2>
    <p v-if="!meeting.rounds.length" class="rounds__muted">{{ t('team.meetings.noAnswers') }}</p>
    <details
      v-for="(round, i) in [...meeting.rounds].reverse()"
      :key="round.n"
      class="round"
      :open="i === 0"
    >
      <summary class="round__head">{{ t('team.meetings.roundN', { n: round.n }) }}</summary>
      <p v-if="!round.answers.length" class="rounds__muted">{{ t('team.meetings.noAnswers') }}</p>
      <article v-for="a in round.answers" :key="a.label" class="answer">
        <template v-if="a.absent">
          <header class="answer__head">
            <h3 class="answer__who">{{ who(a.label) }}</h3>
            <StatusPill size="sm" :tone="absentTone(a)" :label="t(absentKey(a))" />
          </header>
          <details v-if="a.error" class="answer__detail">
            <summary>{{ t('team.meetings.absentDetail') }}</summary>
            <p class="answer__text">{{ a.error }}</p>
          </details>
        </template>
        <template v-else>
          <header class="answer__head">
            <h3 class="answer__who">{{ who(a.label) }}</h3>
            <span v-if="a.option" class="answer__tag">{{
              t('team.meetings.option', { key: a.option })
            }}</span>
            <span v-if="a.confidence !== null" class="answer__tag">
              {{ t('team.meetings.confidence', { n: a.confidence }) }}
            </span>
            <span v-if="a.moved" class="answer__tag">{{
              t(`team.meetings.moved.${a.moved}`)
            }}</span>
          </header>
          <p class="answer__stance">{{ a.stance }}</p>
          <template v-for="key in lists" :key="key">
            <div v-if="listOf(a, key).length" class="answer__block">
              <h4 class="answer__label">{{ t(`team.meetings.${key}`) }}</h4>
              <ul class="answer__list">
                <li v-for="(item, j) in listOf(a, key)" :key="j">{{ item }}</li>
              </ul>
            </div>
          </template>
          <div v-if="a.claims.length" class="answer__block">
            <h4 class="answer__label">{{ t('team.meetings.claims') }}</h4>
            <ClaimList :claims="a.claims" />
          </div>
          <div v-if="a.rebuttal" class="answer__block">
            <h4 class="answer__label">{{ t('team.meetings.rebuttal') }}</h4>
            <p class="answer__text">{{ a.rebuttal }}</p>
          </div>
          <p v-if="a.editedFiles?.length" class="answer__warn tone-attention" role="note">
            {{ t('team.meetings.editedFiles', { files: a.editedFiles.join(', ') }) }}
          </p>
        </template>
      </article>
      <section
        v-if="leadOf(round.n).answers.length || leadOf(round.n).notes.length"
        class="answer lead"
        :aria-labelledby="`lead-${round.n}`"
      >
        <h3 :id="`lead-${round.n}`" class="answer__who">{{ t('team.meetings.yourAnswers') }}</h3>
        <dl v-if="leadOf(round.n).answers.length" class="lead__qa">
          <div v-for="q in leadOf(round.n).answers" :key="q.id">
            <dt>{{ q.question }}</dt>
            <dd>{{ q.answer }}</dd>
          </div>
        </dl>
        <div v-for="(n, j) in leadOf(round.n).notes" :key="j" class="answer__block">
          <h4 class="answer__label">{{ t('team.meetings.yourNote') }}</h4>
          <p class="answer__text">{{ n.text }}</p>
        </div>
      </section>
    </details>
  </section>
</template>

<style scoped>
.rounds {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.rounds__title {
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
}

.rounds__muted {
  color: var(--text-muted);
  font-size: var(--fs-sm);
}

.round {
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
}

.round__head {
  display: flex;
  align-items: center;
  min-height: max(40px, var(--target-min));
  padding: 0 var(--space-4);
  font-weight: var(--fw-medium);
  cursor: pointer;
}

.round__head:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}

.answer {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-4);
  border-top: var(--border-width) solid var(--border-subtle);
  font-size: var(--fs-sm);
}

.answer__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.answer__who {
  font-size: var(--fs-sm);
  font-weight: var(--fw-semibold);
}

.answer__tag {
  padding: 0 var(--space-2);
  border-radius: var(--radius-pill);
  background: var(--bg-surface-2);
  color: var(--text-secondary);
  font-size: var(--fs-2xs);
}

.answer__stance,
.answer__text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.answer__label {
  margin-bottom: var(--space-1);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.answer__list {
  margin: 0;
  padding-left: var(--space-5);
}

.answer__list li {
  overflow-wrap: anywhere;
}

.answer__detail summary {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  cursor: pointer;
}

.answer__detail summary:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}

.answer__detail .answer__text {
  margin-top: var(--space-1);
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
}

.lead__qa {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: 0;
}

.lead__qa dt {
  color: var(--text-secondary);
  overflow-wrap: anywhere;
}

.lead__qa dd {
  margin: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.answer__warn {
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm);
  background: var(--tone-bg);
  color: var(--tone-fg);
}
</style>
