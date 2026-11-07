<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import type { Meeting } from '@/api/types'
import PageHeader from '@/components/layout/PageHeader.vue'
import Button from '@/components/ui/Button.vue'
import ConfirmDialog from '@/components/ui/ConfirmDialog.vue'
import Spinner from '@/components/ui/Spinner.vue'
import StatusPill from '@/components/ui/StatusPill.vue'
import { useFormat } from '@/composables/useFormat'
import { RANK_TONE } from '@/features/members/memberModel'
import { truncateMiddle } from '@/lib/paths'
import { useTeamStore } from '@/stores/team'
import { useToastStore } from '@/stores/toast'

import MeetingLeadPanel from './MeetingLeadPanel.vue'
import { MEETING_STATUS, meetingOpen } from './meetingModel'
import MeetingOutcome from './MeetingOutcome.vue'
import MeetingRounds from './MeetingRounds.vue'
import { useLiveRecord } from './useLiveRecord'

/**
 * One meeting: the question and its options, the attendees, what the lead has to do now (answer, decide, act),
 * the outcome and the rounds. Live over meeting.updated for this id.
 */
const props = defineProps<{ meetingId: string }>()

const { t } = useI18n()
const fmt = useFormat()
const team = useTeamStore()
const toast = useToastStore()

const { record, status, error, reload, set } = useLiveRecord<Meeting>(
  () => props.meetingId,
  (id, signal) => team.api().meeting(id, signal),
  'meeting.updated',
)
const cancelling = ref(false)
const cancelBusy = ref(false)
const cancelError = ref('')

const look = computed(() => (record.value ? MEETING_STATUS[record.value.status] : null))
/** Attendees in label order (P1, P2, … P10), whatever order they joined in. */
const attendees = computed(() =>
  [...(record.value?.attendees ?? [])].sort((a, b) =>
    a.label.localeCompare(b.label, undefined, { numeric: true }),
  ),
)
const sealedShown = computed(() => {
  const m = record.value
  return m?.sealed && m.sealed.text && m.round >= m.sealed.revealedRound ? m.sealed : null
})

async function cancel() {
  cancelBusy.value = true
  cancelError.value = ''
  try {
    set(await team.api().cancelMeeting(props.meetingId))
    cancelling.value = false
    toast.show({ message: t('team.meetings.cancelled'), tone: 'success' })
  } catch (e) {
    cancelError.value = errorMessage(e)
  } finally {
    cancelBusy.value = false
  }
}
</script>

<template>
  <div class="meeting">
    <div class="pad pad--top">
      <Button
        class="meeting__back"
        size="sm"
        variant="ghost"
        icon="arrowLeft"
        :to="{ name: 'meetings' }"
      >
        {{ t('team.meetings.back') }}
      </Button>
      <div v-if="status === 'error' && !record" class="meeting__note" role="alert">
        {{ t('team.meetings.notFound', { error }) }}
        <Button size="sm" variant="ghost" @click="reload()">{{ t('common.action.retry') }}</Button>
      </div>
      <Spinner v-else-if="!record" :label="t('team.meetings.loading')" />
    </div>
    <template v-if="record">
      <PageHeader
        :title="record.title"
        :description="truncateMiddle(record.cwd)"
        :description-title="record.cwd"
      >
        <template #actions>
          <Button v-if="meetingOpen(record.status)" variant="ghost" @click="cancelling = true">
            {{ t('team.meetings.cancel') }}
          </Button>
        </template>
      </PageHeader>
      <div class="pad">
        <div class="meeting__meta">
          <StatusPill
            v-if="look"
            :tone="look.tone"
            :icon="look.icon"
            :label="t(`team.meetings.status.${record.status}`)"
          />
          <span>{{ t('team.meetings.roundOf', { n: record.round, max: record.maxRounds }) }}</span>
          <span>
            {{
              t('team.meetings.usage', {
                runs: fmt.number(record.usage.runs),
                tokens: fmt.compact(record.usage.tokens),
              })
            }}
          </span>
          <span>{{ fmt.dateTime(record.createdAt) }}</span>
        </div>
        <p v-if="record.error" class="meeting__error" role="alert">{{ record.error }}</p>

        <section class="meeting__block">
          <h2 class="meeting__h">{{ t('team.meetings.question') }}</h2>
          <p class="meeting__text">{{ record.question }}</p>
          <template v-if="record.options.length">
            <h3 class="meeting__sub">{{ t('team.meetings.options') }}</h3>
            <ol class="meeting__options">
              <li v-for="o in record.options" :key="o.key">
                <strong>{{ o.key }}.</strong> {{ o.text }}
              </li>
            </ol>
          </template>
          <template v-if="record.context.length">
            <h3 class="meeting__sub">{{ t('team.meetings.context') }}</h3>
            <p class="meeting__files">
              <code v-for="f in record.context" :key="f">{{ f }}</code>
            </p>
          </template>
          <h3 class="meeting__sub">{{ t('team.meetings.attendees') }}</h3>
          <ul class="meeting__attendees">
            <li v-for="a in attendees" :key="a.label" :class="{ 'is-absent': !a.present }">
              <strong>{{ a.label }}</strong>
              {{ a.name || a.member }}
              <StatusPill
                size="sm"
                :tone="RANK_TONE[a.rank]"
                :label="t(`team.rank.${a.rank}`)"
                :title="t(`team.rankHint.${a.rank}`)"
              />
              <span v-if="!a.present" class="meeting__muted">{{ t('team.meetings.absent') }}</span>
            </li>
          </ul>
          <template v-if="sealedShown">
            <h3 class="meeting__sub">{{ t('team.meetings.sealed') }} ({{ sealedShown.label }})</h3>
            <p class="meeting__text">{{ sealedShown.text }}</p>
          </template>
        </section>

        <MeetingLeadPanel :meeting="record" @updated="set" />
        <MeetingOutcome :meeting="record" @updated="set" />
        <MeetingRounds :meeting="record" />
      </div>
    </template>

    <ConfirmDialog
      :open="cancelling"
      :title="t('team.meetings.cancelTitle')"
      :description="t('team.meetings.cancelDesc')"
      :confirm-label="t('team.meetings.cancel')"
      danger
      :busy="cancelBusy"
      :error="cancelError"
      @update:open="cancelling = false"
      @confirm="cancel"
    />
  </div>
</template>

<style scoped>
.meeting {
  display: flex;
  flex-direction: column;
}

.pad {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  padding: 0 var(--space-6) var(--space-7);
}

.pad--top {
  padding-top: var(--space-4);
  padding-bottom: 0;
}

@media (width < 600px) {
  .pad {
    padding-inline: var(--space-4);
  }
}

.meeting__back {
  align-self: flex-start;
}

.meeting__note {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  color: var(--text-secondary);
}

.meeting__meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-4);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.meeting__error {
  color: var(--status-danger-fg);
}

.meeting__block {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-4);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
  font-size: var(--fs-sm);
}

.meeting__h {
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
}

.meeting__sub {
  margin-top: var(--space-2);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.meeting__text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.meeting__options {
  margin: 0;
  padding-left: var(--space-2);
  list-style: none;
}

.meeting__files {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
}

.meeting__attendees {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin: 0;
  padding: 0;
  list-style: none;
}

.meeting__attendees li {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.meeting__attendees .is-absent {
  color: var(--text-muted);
}

.meeting__muted {
  color: var(--text-muted);
}
</style>
