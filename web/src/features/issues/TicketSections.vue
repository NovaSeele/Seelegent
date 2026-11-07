<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { OutboxEntry, Ticket } from '@/api/types'
import Icon from '@/components/ui/Icon.vue'
import StatusPill from '@/components/ui/StatusPill.vue'
import { TASK_STATUS } from '@/components/ui/tone'
import { useFormat } from '@/composables/useFormat'

import { CHECKS_STATE, runState, safeUrl, ticketFinished, ticketTaskStatus } from './issueModel'
import { findingView } from './ticketText'
import TicketLog from './TicketLog.vue'

/**
 * The parts of a ticket below its header: its tasks, the CI checks of the pull request, the changed files (the
 * ones a refused push named are marked), what left the machine (the outbox) and the log. Every text here is plain
 * text (check summaries and log data come from outside); links are http(s) only.
 */
const props = defineProps<{ ticket: Ticket }>()

const { t } = useI18n()
const fmt = useFormat()

const skipped = computed(() => props.ticket.env?.skipped ?? [])
const megabytes = (bytes: number) =>
  fmt.value.number(bytes / 1_048_576, { style: 'unit', unit: 'megabyte', maximumFractionDigits: 1 })
/** The changed files a refused push named, with what each one is. */
const refused = computed(
  () =>
    new Map(
      (props.ticket.refusal?.findings ?? []).map((f) => {
        const v = findingView(f, megabytes)
        return [f.path, t(v.what.key, v.what.params)] as const
      }),
    ),
)

/**
 * The failed step of the refused push, if the refusal stands: its reason is in the banner above, in the page's
 * language, so the step does not repeat the daemon's English text.
 */
const refusedStep = computed(() => {
  const at = props.ticket.refusal?.at
  if (!at) return undefined
  const when = Date.parse(at)
  return props.ticket.outbox
    .filter((o) => o.status === 'failed' && Math.abs(Date.parse(o.doneAt ?? o.at) - when) < 60_000)
    .at(-1)?.id
})

/** An outbox step's state: a compare link made for the manual tier is "link ready", not "done". */
function outboxState(o: OutboxEntry): string {
  if (o.id === refusedStep.value) return 'refused'
  if (o.status === 'done' && o.kind === 'open_pr' && o.result.manualUrl) return 'linkReady'
  if (o.status === 'done' && o.result.skipped) return 'skipped'
  return o.status
}
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
        <span v-if="run.app" class="item__meta">{{ run.app }}</span>
        <StatusPill
          size="sm"
          :tone="CHECKS_STATE[runState(run)].tone"
          :icon="CHECKS_STATE[runState(run)].icon"
          :label="t(`issues.ticket.runState.${runState(run)}`)"
        />
        <p v-if="run.summary" class="item__text">{{ run.summary }}</p>
      </li>
    </ul>
    <p v-if="ticket.checksFix && !ticketFinished(ticket.status)" class="sec__none">
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
      <li
        v-for="f in ticket.files"
        :key="f.path"
        class="file"
        :class="{ 'file--refused tone-attention': refused.has(f.path) }"
      >
        <span class="file__status" :title="t('issues.ticket.fileStatus')">{{ f.status }}</span>
        <code class="file__path">{{ f.from ? `${f.from} → ${f.path}` : f.path }}</code>
        <span v-if="refused.has(f.path)" class="file__flag">
          <Icon name="warning" />
          {{ t('issues.ticket.fileRefused', { what: refused.get(f.path) ?? '' }) }}
        </span>
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
            {{ t(`issues.ticket.outboxStatus.${outboxState(o)}`) }}
          </span>
          <span>{{ fmt.dateTime(o.at) }}</span>
        </span>
        <p v-if="o.id === refusedStep" class="item__text">{{ t('issues.ticket.outboxRefused') }}</p>
        <p v-else-if="o.error" class="item__text item__bad">{{ o.error }}</p>
      </li>
    </ul>
  </section>

  <TicketLog :entries="ticket.log" />
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

.file--refused {
  margin-inline: calc(-1 * var(--space-2));
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm);
  background: var(--tone-bg);
}

.file__flag {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  color: var(--tone-fg);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

/* Touch screens: the task and check links are at least a finger high. */
@media (pointer: coarse) {
  .item__link {
    display: inline-flex;
    align-items: center;
    min-height: var(--target-min);
  }
}
</style>
