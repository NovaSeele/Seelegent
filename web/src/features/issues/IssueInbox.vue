<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { IssueState } from '@/api/github'
import type { Issue } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Chip from '@/components/ui/Chip.vue'
import Icon from '@/components/ui/Icon.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import SegmentedControl, { type SegmentOption } from '@/components/ui/SegmentedControl.vue'
import Spinner from '@/components/ui/Spinner.vue'
import StatusPill from '@/components/ui/StatusPill.vue'
import { useFormat } from '@/composables/useFormat'
import { useGitHubStore } from '@/stores/github'

import AddIssueDialog from './AddIssueDialog.vue'
import { causeKey } from './causeText'
import { inboxOrder, phaseLabel, safeUrl, TICKET_PHASE, ticketPhase } from './issueModel'
import TriageDialog from './TriageDialog.vue'

/**
 * The chosen repo's inbox: GitHub issues from the last sync and local ones, undecided first. Issue text is shown
 * as plain text (it is untrusted); a decision is made in the triage dialog. Empty after a failed sync, it says so
 * with the cause's next step (the repo bar above keeps the daemon's message), not "sync to fetch".
 */
const { t } = useI18n()
const fmt = useFormat()
const gh = useGitHubStore()

const adding = ref(false)
const triaging = ref(false)
const chosen = ref<Issue | null>(null)

const states = computed<SegmentOption<IssueState>[]>(() =>
  (['open', 'closed', 'all'] as const).map((value) => ({
    value,
    label: t(`issues.inbox.state.${value}`),
  })),
)
const state = computed({
  get: () => gh.issueState,
  set: (v: IssueState) => gh.setIssueState(v),
})
const shown = computed(() => inboxOrder(gh.issues))

function decide(issue: Issue) {
  chosen.value = issue
  triaging.value = true
}
</script>

<template>
  <div class="inbox">
    <div class="inbox__bar">
      <SegmentedControl
        v-model="state"
        :options="states"
        :label="t('issues.inbox.stateLabel')"
        size="sm"
      />
      <Button size="sm" icon="plus" @click="adding = true">{{ t('issues.inbox.add') }}</Button>
    </div>
    <Spinner v-if="gh.issuesStatus === 'loading'" :label="t('issues.inbox.loading')" />
    <p v-else-if="gh.issuesStatus === 'error'" class="inbox__note" role="alert">
      {{ t('issues.inbox.loadFailed', { error: gh.issuesError }) }}
      <Button size="sm" variant="ghost" @click="gh.loadIssues()">{{
        t('common.action.retry')
      }}</Button>
    </p>
    <EmptyState
      v-else-if="!shown.length && gh.repo?.syncError"
      icon="warning"
      :title="t('issues.inbox.syncFailed')"
      :description="t('issues.inbox.syncFailedDesc', { next: t(causeKey(gh.repo.syncErrorCause)) })"
    />
    <EmptyState
      v-else-if="!shown.length"
      icon="issues"
      :title="t('issues.inbox.empty')"
      :description="t('issues.inbox.emptyDesc')"
    />
    <ul v-else class="inbox__list">
      <li v-for="i in shown" :key="i.key" class="issue">
        <div class="issue__head">
          <span class="issue__key">{{ i.key }}</span>
          <a
            v-if="safeUrl(i.url)"
            class="issue__title"
            :href="safeUrl(i.url)"
            target="_blank"
            rel="noopener noreferrer"
          >
            {{ i.title }}
          </a>
          <span v-else class="issue__title">{{ i.title }}</span>
          <StatusPill
            v-if="i.state === 'closed'"
            size="sm"
            tone="muted"
            :label="t('issues.inbox.closed')"
          />
        </div>
        <div class="issue__meta">
          <Chip v-for="l in i.labels" :key="l">{{ l }}</Chip>
          <span v-if="i.author">{{ t('issues.inbox.by', { author: i.author }) }}</span>
          <span v-if="i.updatedAt">{{ fmt.relative(i.updatedAt) }}</span>
        </div>
        <p v-if="i.suggestion && !i.decision" class="issue__line">
          {{
            t('issues.inbox.suggested', {
              action: t(`issues.triage.action.${i.suggestion.action}`),
            })
          }}
          <span v-if="i.suggestion.reason"> — {{ i.suggestion.reason }}</span>
        </p>
        <p v-if="i.decision" class="issue__line">
          {{
            t('issues.inbox.decided', {
              action: t(`issues.triage.action.${i.decision.action}`),
              by: t(`issues.inbox.who.${i.decision.by}`),
            })
          }}
          <span v-if="i.decision.reason"> — {{ i.decision.reason }}</span>
        </p>
        <details v-if="i.body" class="issue__body">
          <summary>{{ t('issues.inbox.body') }}</summary>
          <p class="issue__text">{{ i.body }}</p>
        </details>
        <div class="issue__actions">
          <RouterLink
            v-if="i.ticket"
            class="issue__ticket"
            :to="{ name: 'issues', params: { ticketId: i.ticket.id } }"
            :aria-label="t('issues.inbox.ticket', { status: t(phaseLabel(ticketPhase(i.ticket))) })"
          >
            <Icon name="git" class="issue__ticketIcon" />
            <StatusPill
              size="sm"
              :tone="TICKET_PHASE[ticketPhase(i.ticket)].tone"
              :icon="TICKET_PHASE[ticketPhase(i.ticket)].icon"
              :label="t(phaseLabel(ticketPhase(i.ticket)))"
            />
          </RouterLink>
          <Button
            v-else-if="i.state === 'open' && i.decision?.action !== 'skip'"
            size="sm"
            variant="primary"
            @click="decide(i)"
          >
            {{ t('issues.inbox.decide') }}
          </Button>
        </div>
      </li>
    </ul>
    <AddIssueDialog v-model:open="adding" />
    <TriageDialog v-model:open="triaging" :issue="chosen" />
  </div>
</template>

<style scoped>
.inbox {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.inbox__bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.inbox__note {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.inbox__list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.issue {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding: var(--space-3) var(--space-4);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
}

.issue__head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-2);
}

.issue__key {
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
}

.issue__title {
  flex: 1;
  min-width: 0;
  font-weight: var(--fw-medium);
  overflow-wrap: anywhere;
}

.issue__meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) var(--space-3);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.issue__line {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  overflow-wrap: anywhere;
}

.issue__body summary {
  cursor: pointer;
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.issue__body summary:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}

.issue__text {
  max-height: 320px;
  margin-top: var(--space-2);
  overflow: auto;
  font-size: var(--fs-sm);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.issue__actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
}

.issue__ticket {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  min-height: var(--target-min);
  border-radius: var(--radius-sm);
  color: var(--text-secondary);
  text-decoration: none;
}

/* Touch screens: the issue text toggle is at least a finger high. */
@media (pointer: coarse) {
  .issue__body summary {
    display: flex;
    align-items: center;
    min-height: var(--target-min);
  }
}

.issue__ticket:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}
</style>
