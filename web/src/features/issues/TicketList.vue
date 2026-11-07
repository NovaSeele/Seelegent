<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { TicketSummary } from '@/api/types'
import EmptyState from '@/components/ui/EmptyState.vue'
import Spinner from '@/components/ui/Spinner.vue'
import StatusPill from '@/components/ui/StatusPill.vue'
import { useFormat } from '@/composables/useFormat'
import { useGitHubStore } from '@/stores/github'

import { needsYou, phaseLabel, TICKET_PHASE, ticketPhase } from './issueModel'

/**
 * The chosen repo's tickets, newest change first, live over ticket.updated: the ones waiting for you (a task to
 * review, a pull request to open, a refused push, failed checks, a failed start) in their own group on top.
 */
const { t } = useI18n()
const fmt = useFormat()
const gh = useGitHubStore()

const shown = computed(() =>
  gh.tickets
    .filter((x) => x.repoId === gh.repoId)
    .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt)),
)
const groups = computed(() => {
  const waiting = shown.value.filter((x) => needsYou(ticketPhase(x)))
  const rest = shown.value.filter((x) => !needsYou(ticketPhase(x)))
  return [
    { key: 'needsYou', items: waiting },
    { key: 'others', items: rest },
  ].filter((g) => g.items.length > 0)
})
const look = (x: TicketSummary) => TICKET_PHASE[ticketPhase(x)]
</script>

<template>
  <Spinner v-if="gh.ticketsStatus === 'loading'" :label="t('issues.tickets.loading')" />
  <EmptyState
    v-else-if="!shown.length"
    icon="git"
    :title="t('issues.tickets.empty')"
    :description="t('issues.tickets.emptyDesc')"
  />
  <div v-else class="groups">
    <section v-for="g in groups" :key="g.key" class="group" :aria-labelledby="`tickets-${g.key}`">
      <h2 :id="`tickets-${g.key}`" class="group__h">
        {{ t(`issues.tickets.group.${g.key}`, { count: g.items.length }) }}
      </h2>
      <ul class="list">
        <li v-for="x in g.items" :key="x.id">
          <RouterLink class="row" :to="{ name: 'issues', params: { ticketId: x.id } }">
            <span class="row__main">
              <span class="row__title">{{ x.issue.title }}</span>
              <span class="row__meta">
                <span class="row__mono">{{ x.issueKey }}</span>
                <span class="row__mono">{{ x.branch }}</span>
                <span v-if="x.pr?.number">{{ t('issues.tickets.pr', { n: x.pr.number }) }}</span>
                <span>{{ fmt.relative(x.updatedAt) }}</span>
              </span>
            </span>
            <StatusPill
              size="sm"
              :tone="look(x).tone"
              :icon="look(x).icon"
              :label="t(phaseLabel(ticketPhase(x)))"
            />
          </RouterLink>
        </li>
      </ul>
    </section>
  </div>
</template>

<style scoped>
.groups {
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
}

.group {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.group__h {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  font-weight: var(--fw-semibold);
}

.list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
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

.row__mono {
  font-family: var(--font-mono);
  overflow-wrap: anywhere;
}

@media (width < 600px) {
  .row {
    flex-wrap: wrap;
  }
}
</style>
