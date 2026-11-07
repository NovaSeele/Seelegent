<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Ticket } from '@/api/types'
import StatusPill from '@/components/ui/StatusPill.vue'
import { TASK_STATUS } from '@/components/ui/tone'
import { useFormat } from '@/composables/useFormat'

import { failingChecks, logText, safeUrl, ticketTaskStatus } from './issueModel'

/**
 * The parts of a ticket below its header: its tasks, the CI checks of the pull request, the changed files, what
 * left the machine (the outbox) and the log. Every text here is plain text (check summaries and log data come
 * from outside); links are http(s) only.
 */
const props = defineProps<{ ticket: Ticket }>()

const { t } = useI18n()
const fmt = useFormat()

const failing = computed(() => new Set(failingChecks(props.ticket.checks)))
const log = computed(() => [...props.ticket.log].reverse())
const skipped = computed(() => props.ticket.env?.skipped ?? [])
</script>

<template>
  <section class="sec" :aria-label="t('issues.ticket.tasks')">
    <h2 class="sec__h">{{ t('issues.ticket.tasks') }}</h2>
    <p v-if="!ticket.tasks.length" class="sec__none">{{ t('issues.ticket.noTasks') }}</p>
    <ul v-else class="sec__list">
      <li v-for="task in ticket.tasks" :key="task.id" class="item">
        <RouterLink class="item__link" :to="{ name: 'tasks', params: { taskId: task.id } }">
          {{ task.title || task.id }}
        </RouterLink>
        <span class="item__meta">
          <span v-if="task.member">{{ task.member }}</span>
          <span>{{ t('issues.ticket.turns', { n: task.turns }, task.turns) }}</span>
          <span>{{ fmt.relative(task.updatedAt) }}</span>
        </span>
        <StatusPill
          size="sm"
          :tone="TASK_STATUS[ticketTaskStatus(task)].tone"
          :icon="TASK_STATUS[ticketTaskStatus(task)].icon"
          :label="t(`common.status.${ticketTaskStatus(task)}.label`)"
        />
      </li>
    </ul>
  </section>

  <section v-if="ticket.checks" class="sec" :aria-label="t('issues.ticket.checks')">
    <h2 class="sec__h">{{ t('issues.ticket.checks') }}</h2>
    <p v-if="ticket.checks.error" class="sec__error" role="alert">{{ ticket.checks.error }}</p>
    <p v-if="!ticket.checks.runs.length" class="sec__none">{{ t('issues.ticket.noChecks') }}</p>
    <ul v-else class="sec__list">
      <li v-for="run in ticket.checks.runs" :key="`${run.kind}-${run.id ?? run.name}`" class="item">
        <a
          v-if="safeUrl(run.url)"
          class="item__link"
          :href="safeUrl(run.url)"
          target="_blank"
          rel="noopener noreferrer"
        >
          {{ run.name }}
        </a>
        <span v-else class="item__name">{{ run.name }}</span>
        <span class="item__meta">
          <span v-if="run.app">{{ run.app }}</span>
          <span :class="{ item__bad: failing.has(run.name) }">{{
            run.conclusion || run.status
          }}</span>
        </span>
        <p v-if="run.summary" class="item__text">{{ run.summary }}</p>
      </li>
    </ul>
    <p v-if="ticket.checksFix" class="sec__none">
      {{ t('issues.ticket.checksFix', { runs: ticket.checksFix.runs.join(', ') }) }}
      <RouterLink :to="{ name: 'tasks', params: { taskId: ticket.checksFix.taskId } }">
        {{ ticket.checksFix.taskId }}
      </RouterLink>
    </p>
  </section>

  <section class="sec" :aria-label="t('issues.ticket.files')">
    <h2 class="sec__h">{{ t('issues.ticket.files') }}</h2>
    <p v-if="!ticket.files.length" class="sec__none">{{ t('issues.ticket.noFiles') }}</p>
    <ul v-else class="sec__list sec__list--dense">
      <li v-for="f in ticket.files" :key="f.path" class="file">
        <span class="file__status" :title="t('issues.ticket.fileStatus')">{{ f.status }}</span>
        <code class="file__path">{{ f.from ? `${f.from} → ${f.path}` : f.path }}</code>
      </li>
    </ul>
    <template v-if="skipped.length">
      <h3 class="sec__sub">{{ t('issues.ticket.envSkipped') }}</h3>
      <ul class="sec__list sec__list--dense">
        <li v-for="s in skipped" :key="s.path" class="file">
          <code class="file__path">{{ s.path }}</code>
          <span class="item__meta">{{ s.reason }}</span>
        </li>
      </ul>
    </template>
  </section>

  <section v-if="ticket.outbox.length" class="sec" :aria-label="t('issues.ticket.outbox')">
    <h2 class="sec__h">{{ t('issues.ticket.outbox') }}</h2>
    <ul class="sec__list sec__list--dense">
      <li v-for="o in ticket.outbox" :key="o.id" class="item">
        <span class="item__name">{{ t(`issues.ticket.outboxKind.${o.kind}`) }}</span>
        <span class="item__meta">
          <span :class="{ item__bad: o.status === 'failed' || o.status === 'unknown' }">
            {{ t(`issues.ticket.outboxStatus.${o.status}`) }}
          </span>
          <span>{{ fmt.dateTime(o.at) }}</span>
        </span>
        <p v-if="o.error" class="item__text item__bad">{{ o.error }}</p>
      </li>
    </ul>
  </section>

  <section v-if="log.length" class="sec" :aria-label="t('issues.ticket.log')">
    <details>
      <summary class="sec__h sec__summary">{{ t('issues.ticket.log') }} ({{ log.length }})</summary>
      <ol class="sec__list sec__list--dense">
        <li v-for="e in log" :key="e.id" class="log">
          <span class="log__at">{{ fmt.dateTime(e.at) }}</span>
          <code class="log__type">{{ e.type }}</code>
          <span class="log__text">{{ logText(e.data) }}</span>
        </li>
      </ol>
    </details>
  </section>
</template>

<style scoped>
.sec {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-4);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
}

.sec__h {
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
}

.sec__sub {
  margin-top: var(--space-2);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  font-weight: var(--fw-medium);
}

.sec__summary {
  cursor: pointer;
  border-radius: var(--radius-sm);
}

.sec__summary:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}

.sec__none {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  overflow-wrap: anywhere;
}

.sec__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
  overflow-wrap: anywhere;
}

.sec__list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.sec__list--dense {
  gap: var(--space-1);
  margin-top: var(--space-2);
}

.item {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) var(--space-3);
}

.item__link,
.item__name {
  flex: 1 1 200px;
  min-width: 0;
  font-weight: var(--fw-medium);
  overflow-wrap: anywhere;
}

.item__link {
  color: var(--link);
}

.item__meta {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-3);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.item__bad {
  color: var(--status-danger-fg);
}

.item__text {
  flex-basis: 100%;
  max-height: 160px;
  overflow: auto;
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.file {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-2);
}

.file__status {
  min-width: 1.5em;
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
}

.file__path {
  min-width: 0;
  font-size: var(--fs-xs);
  overflow-wrap: anywhere;
}

.log {
  display: grid;
  grid-template-columns: auto auto 1fr;
  gap: var(--space-2);
  font-size: var(--fs-xs);
}

.log__at {
  color: var(--text-muted);
  white-space: nowrap;
}

.log__text {
  min-width: 0;
  color: var(--text-secondary);
  overflow-wrap: anywhere;
}

@media (width < 600px) {
  .log {
    grid-template-columns: 1fr;
    gap: 0;
  }
}
</style>
