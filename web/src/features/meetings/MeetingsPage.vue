<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import Button from '@/components/ui/Button.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Spinner from '@/components/ui/Spinner.vue'
import StatusPill from '@/components/ui/StatusPill.vue'
import { useFormat } from '@/composables/useFormat'
import { useTeamStore } from '@/stores/team'

import MeetingDetail from './MeetingDetail.vue'
import { MEETING_STATUS } from './meetingModel'
import OpenMeetingDialog from './OpenMeetingDialog.vue'

/**
 * Meetings: the list (newest first, live over meeting.updated) or, with a meeting id, that meeting. The route
 * passes its parameter as a prop (props: true), so it follows the page on screen under the settings overlay.
 */
const props = defineProps<{ meetingId?: string }>()

const { t } = useI18n()
const fmt = useFormat()
const team = useTeamStore()
const opening = ref(false)

onMounted(() => team.ensureMeetings())

const shown = computed(() =>
  [...team.meetings].sort((a, b) => b.createdAt.localeCompare(a.createdAt)),
)
const look = (status: keyof typeof MEETING_STATUS) => MEETING_STATUS[status]
</script>

<template>
  <PageContainer>
    <MeetingDetail v-if="props.meetingId" :key="props.meetingId" :meeting-id="props.meetingId" />
    <template v-else>
      <PageHeader :title="t('nav.meetings.label')" :description="t('nav.meetings.desc')">
        <template #actions>
          <Button variant="primary" icon="plus" @click="opening = true">
            {{ t('team.meetings.new') }}
          </Button>
        </template>
      </PageHeader>
      <div class="pad">
        <Spinner v-if="team.meetingsStatus === 'loading'" :label="t('team.meetings.loading')" />
        <div v-else-if="team.meetingsStatus === 'error'" class="list__note" role="alert">
          {{ t('team.meetings.loadFailed', { error: team.meetingsError }) }}
          <Button size="sm" variant="ghost" @click="team.loadMeetings()">
            {{ t('common.action.retry') }}
          </Button>
        </div>
        <EmptyState
          v-else-if="!shown.length"
          icon="meetings"
          :title="t('team.meetings.empty')"
          :description="t('team.meetings.emptyDesc')"
        />
        <ul v-else class="list">
          <li v-for="m in shown" :key="m.id">
            <RouterLink class="row" :to="{ name: 'meetings', params: { meetingId: m.id } }">
              <span class="row__main">
                <span class="row__title">{{ m.title }}</span>
                <span class="row__meta">
                  <span>{{ t('team.meetings.roundOf', { n: m.round, max: m.maxRounds }) }}</span>
                  <span>{{ t('team.meetings.attendeesN', { n: m.attendees }, m.attendees) }}</span>
                  <span v-if="m.pendingQuestions" class="row__warn">
                    {{ t('team.meetings.pendingN', { n: m.pendingQuestions }, m.pendingQuestions) }}
                  </span>
                  <span v-if="m.dissents">
                    {{ t('team.meetings.dissentsN', { n: m.dissents }, m.dissents) }}
                  </span>
                  <span>{{ fmt.dateTime(m.createdAt) }}</span>
                </span>
              </span>
              <StatusPill
                size="sm"
                :tone="look(m.status).tone"
                :icon="look(m.status).icon"
                :label="t(`team.meetings.status.${m.status}`)"
              />
            </RouterLink>
          </li>
        </ul>
      </div>
      <OpenMeetingDialog v-model:open="opening" />
    </template>
  </PageContainer>
</template>

<style scoped>
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

.list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.list__note {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.row {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  min-height: max(56px, var(--target-min));
  padding: var(--space-3) var(--space-4);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
  color: inherit;
  text-decoration: none;
}

.row:hover {
  background: var(--bg-hover);
}

.row:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}

.row__main {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.row__title {
  font-weight: var(--fw-medium);
  overflow-wrap: anywhere;
}

.row__meta {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-3);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.row__warn {
  color: var(--attention-fg);
  font-weight: var(--fw-medium);
}
</style>
