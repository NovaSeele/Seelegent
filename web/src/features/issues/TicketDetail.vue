<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import PageHeader from '@/components/layout/PageHeader.vue'
import Button from '@/components/ui/Button.vue'
import Spinner from '@/components/ui/Spinner.vue'
import StatusPill from '@/components/ui/StatusPill.vue'
import { useFormat } from '@/composables/useFormat'
import { useLiveRecord } from '@/features/meetings/useLiveRecord'
import { useGitHubStore } from '@/stores/github'

import { CHECKS_STATE, safeUrl, TICKET_STATUS, ticketFinished } from './issueModel'
import TicketActions from './TicketActions.vue'
import TicketSections from './TicketSections.vue'

/**
 * One ticket: its issue, branch and worktree, the pull request and its checks, what the user can do now, and
 * below that its tasks, files, outbox and log. Live over ticket.updated for this id.
 */
const props = defineProps<{ ticketId: string }>()

const { t } = useI18n()
const fmt = useFormat()
const gh = useGitHubStore()

const { record, status, error, reload, set } = useLiveRecord(
  () => props.ticketId,
  (id, signal) => gh.api().ticket(id, signal),
  'ticket.updated',
)

const repo = computed(() => gh.repos.find((r) => r.id === record.value?.repoId) ?? null)
const look = computed(() => (record.value ? TICKET_STATUS[record.value.status] : null))
// A finished ticket's checks are no longer read: its header does not shout the last state (F38).
const checks = computed(() =>
  record.value?.checks && !ticketFinished(record.value.status)
    ? CHECKS_STATE[record.value.checks.state]
    : null,
)
const prUrl = computed(() => safeUrl(record.value?.pr?.url))
const manualUrl = computed(() => safeUrl(record.value?.pr?.manualUrl))
const issueUrl = computed(() => safeUrl(record.value?.issue.url))
</script>

<template>
  <div class="ticket">
    <div class="pad pad--top">
      <Button
        class="ticket__back"
        size="sm"
        variant="ghost"
        icon="arrowLeft"
        :to="{ name: 'issues' }"
      >
        {{ t('issues.ticket.back') }}
      </Button>
      <div v-if="status === 'error' && !record" class="ticket__note" role="alert">
        {{ t('issues.ticket.notFound', { error }) }}
        <Button size="sm" variant="ghost" @click="reload()">{{ t('common.action.retry') }}</Button>
      </div>
      <Spinner v-else-if="!record" :label="t('issues.ticket.loading')" />
    </div>
    <template v-if="record">
      <PageHeader
        :title="record.issue.title"
        :description="`${record.issueKey} · ${record.branch}`"
      />
      <div class="pad">
        <div class="ticket__meta">
          <StatusPill
            v-if="look"
            :tone="look.tone"
            :icon="look.icon"
            :label="t(`issues.ticket.status.${record.status}`)"
          />
          <StatusPill
            v-if="checks && record.checks"
            :tone="checks.tone"
            :icon="checks.icon"
            :label="t(`issues.ticket.checksState.${record.checks.state}`)"
          />
          <a v-if="issueUrl" :href="issueUrl" target="_blank" rel="noopener noreferrer">
            {{ t('issues.ticket.issueLink', { key: record.issueKey }) }}
          </a>
          <a v-if="prUrl" :href="prUrl" target="_blank" rel="noopener noreferrer">
            {{ t('issues.ticket.prLink', { n: record.pr?.number ?? '' }) }}
            <span v-if="record.pr?.draft">({{ t('issues.ticket.draft') }})</span>
          </a>
          <a v-else-if="manualUrl" :href="manualUrl" target="_blank" rel="noopener noreferrer">
            {{ t('issues.ticket.compareLink') }}
          </a>
          <RouterLink
            v-if="record.meetingId"
            :to="{ name: 'meetings', params: { meetingId: record.meetingId } }"
          >
            {{ t('issues.ticket.meeting') }}
          </RouterLink>
          <span>{{ fmt.relative(record.updatedAt) }}</span>
        </div>
        <p v-if="record.error" class="ticket__error" role="alert">{{ record.error }}</p>
        <p v-if="record.dropReason" class="ticket__note">
          {{ t('issues.ticket.dropReason', { reason: record.dropReason }) }}
        </p>
        <p v-if="manualUrl && !prUrl" class="ticket__note">{{ t('issues.ticket.manualHint') }}</p>
        <dl class="ticket__facts">
          <dt>{{ t('issues.ticket.worktree') }}</dt>
          <dd>
            <code>{{ record.worktree }}</code>
            <span v-if="record.worktreeRemoved" class="ticket__muted">
              {{ t('issues.ticket.removed') }}
            </span>
          </dd>
          <dt>{{ t('issues.ticket.base') }}</dt>
          <dd>
            <code>{{ record.base }}</code>
          </dd>
        </dl>
        <TicketActions :ticket="record" :repo="repo" @changed="set" />
        <TicketSections :ticket="record" />
      </div>
    </template>
  </div>
</template>

<style scoped>
.ticket {
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

.ticket__back {
  align-self: flex-start;
}

.ticket__note {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  overflow-wrap: anywhere;
}

.ticket__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.ticket__meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-3);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.ticket__meta a {
  color: var(--link);
}

.ticket__meta a:focus-visible {
  outline: none;
  border-radius: var(--radius-sm);
  box-shadow: var(--focus-ring);
}

.ticket__facts {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: var(--space-1) var(--space-3);
  margin: 0;
  font-size: var(--fs-sm);
}

.ticket__facts dt {
  color: var(--text-secondary);
}

.ticket__facts dd {
  min-width: 0;
  margin: 0;
  overflow-wrap: anywhere;
}

.ticket__muted {
  margin-inline-start: var(--space-2);
  color: var(--text-muted);
}

@media (width < 600px) {
  .ticket__facts {
    grid-template-columns: 1fr;
  }
}
</style>
