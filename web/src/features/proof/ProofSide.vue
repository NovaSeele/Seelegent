<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import type { LanesView, ProofStats } from '@/api/types'
import { useFormat } from '@/composables/useFormat'

/**
 * The lane claims held in the repository (which task may change which files) and what the gates did: red first
 * runs, fixes before review, lane refusals, the verifier's verdicts.
 */
const props = defineProps<{ lanes: LanesView | null; stats: ProofStats | null }>()

const { t } = useI18n()
const fmt = useFormat()

const statRows = computed(() => {
  const s = props.stats
  if (!s) return []
  const v = s.verifier
  return [
    { key: 'tasks', value: t('proof.stats.tasksValue', { gated: s.gatedTasks, total: s.tasks }) },
    { key: 'gateRuns', value: String(s.gateRuns) },
    { key: 'firstRunRed', value: String(s.firstRunRed) },
    { key: 'caught', value: String(s.caughtBeforeReview) },
    {
      key: 'autoFix',
      value: t('proof.stats.autoFixValue', { n: s.autoFixTurns, out: s.exhaustedBudgets }),
    },
    {
      key: 'lanes',
      value: t('proof.stats.lanesValue', {
        refused: s.laneRefusals,
        forced: s.laneForced,
        caught: s.laneViolationsCaught,
      }),
    },
    {
      key: 'followups',
      value: t('proof.stats.followupsValue', {
        gated: fmt.value.number(s.leadFollowups.gated, { maximumFractionDigits: 2 }),
        ungated: fmt.value.number(s.leadFollowups.ungated, { maximumFractionDigits: 2 }),
      }),
    },
    {
      key: 'verifier',
      value: t('proof.stats.verifierValue', {
        runs: v.runs,
        accepts: v.accepts,
        rejects: v.rejects,
        slipped: v.slipped,
      }),
    },
  ]
})
</script>

<template>
  <section v-if="lanes" class="side" :aria-label="t('proof.lanes.title')">
    <h2 class="side__title">
      {{ t('proof.lanes.title') }}
      <span class="side__muted">{{ t(`proof.lanes.mode.${lanes.mode}`) }}</span>
    </h2>
    <p v-if="!lanes.claims.length" class="side__muted">{{ t('proof.lanes.empty') }}</p>
    <ul v-else class="side__list">
      <li v-for="c in lanes.claims" :key="c.taskId" class="side__claim">
        <RouterLink :to="{ name: 'tasks', params: { taskId: c.taskId } }" class="side__link">
          {{ c.title || c.taskId }}
        </RouterLink>
        <span class="side__muted">
          {{ t(`proof.lanes.status.${c.status}`) }} · {{ t(`proof.lanes.source.${c.source}`) }}
          <template v-if="c.heldUntil">
            · {{ t('proof.lanes.heldUntil', { time: fmt.dateTime(c.heldUntil) }) }}</template
          >
        </span>
        <span class="side__paths">
          <code v-for="p in c.paths" :key="p">{{ p }}</code>
        </span>
      </li>
    </ul>
  </section>
  <section v-if="stats" class="side" :aria-label="t('proof.stats.title')">
    <h2 class="side__title">{{ t('proof.stats.title') }}</h2>
    <dl class="side__stats">
      <template v-for="row in statRows" :key="row.key">
        <dt>{{ t(`proof.stats.${row.key}`) }}</dt>
        <dd>{{ row.value }}</dd>
      </template>
    </dl>
  </section>
</template>

<style scoped>
.side {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}

.side__title {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-2);
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
}

.side__muted {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-regular);
}

.side__list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.side__claim {
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: var(--fs-sm);
}

.side__link {
  color: var(--accent);
  overflow-wrap: anywhere;
}

.side__paths {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-2);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  overflow-wrap: anywhere;
}

.side__stats {
  display: grid;
  grid-template-columns: minmax(8rem, max-content) 1fr;
  gap: var(--space-1) var(--space-3);
  margin: 0;
  font-size: var(--fs-sm);
}

.side__stats dt {
  color: var(--text-secondary);
}

.side__stats dd {
  margin: 0;
}

@media (pointer: coarse) {
  .side__link {
    display: inline-flex;
    align-items: center;
    min-height: var(--target-min);
  }
}
</style>
