<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type { Contest, ScoreRow } from '@/api/types'
import IconButton from '@/components/ui/IconButton.vue'
import { useFormat } from '@/composables/useFormat'

/**
 * The machine score of each variant, in the machine's ranking order: gate, auto-fix turns, files, lines, lane,
 * tokens, time and verifier. A disqualified variant says why in words. The proposed winner and the picked one are
 * marked. Each row opens the variant's changes.
 */
const props = defineProps<{ contest: Contest; selected: string | null }>()
const emit = defineEmits<{ select: [label: string] }>()

const { t } = useI18n()
const fmt = useFormat()

function ordered(): ScoreRow[] {
  const rank = new Map(props.contest.machineRanking.map((l, i) => [l, i]))
  return [...props.contest.scoreboard].sort(
    (a, b) =>
      (rank.get(a.label) ?? 99) - (rank.get(b.label) ?? 99) || a.label.localeCompare(b.label),
  )
}

const runsOn = (r: ScoreRow) =>
  [r.member, r.provider ?? r.backend, r.model].filter((x): x is string => !!x).join(' · ')
const mark = (label: string) =>
  label === props.contest.winner
    ? t('team.contests.winner')
    : label === props.contest.proposal?.winner
      ? t('team.contests.proposal')
      : ''
</script>

<template>
  <div class="board">
    <table class="board__table">
      <caption class="sr-only">
        {{
          t('team.contests.scoreboard')
        }}
      </caption>
      <thead>
        <tr>
          <th scope="col">{{ t('team.contests.col.variant') }}</th>
          <th scope="col">{{ t('team.contests.col.runsOn') }}</th>
          <th scope="col">{{ t('team.contests.col.status') }}</th>
          <th scope="col">{{ t('team.contests.col.gate') }}</th>
          <th scope="col" class="num">{{ t('team.contests.col.fixes') }}</th>
          <th scope="col" class="num">{{ t('team.contests.col.files') }}</th>
          <th scope="col" class="num">{{ t('team.contests.col.lines') }}</th>
          <th scope="col" class="num">{{ t('team.contests.col.outOfLane') }}</th>
          <th scope="col" class="num">{{ t('team.contests.col.tokens') }}</th>
          <th scope="col" class="num">{{ t('team.contests.col.duration') }}</th>
          <th scope="col">{{ t('team.contests.col.verifier') }}</th>
          <th scope="col">
            <span class="sr-only">{{ t('team.contests.diff', { label: '' }) }}</span>
          </th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="r in ordered()"
          :key="r.label"
          :class="{ 'is-out': r.disqualified, 'is-selected': selected === r.label }"
        >
          <th scope="row">
            <span class="board__label">{{ r.label }}</span>
            <span v-if="mark(r.label)" class="board__mark tone-success">{{ mark(r.label) }}</span>
          </th>
          <td>{{ runsOn(r) }}</td>
          <td>
            <span v-if="r.disqualified" class="board__out tone-danger">
              {{
                t('team.contests.out', {
                  reason: t(`team.contests.disqualified.${r.disqualified}`),
                })
              }}
            </span>
            <span v-else>{{ r.status }}</span>
          </td>
          <td>{{ r.gate }}</td>
          <td class="num">{{ fmt.number(r.autoFixTurns) }}</td>
          <td class="num">{{ fmt.number(r.files) }}</td>
          <td class="num">
            <span class="add">+{{ fmt.number(r.added) }}</span>
            <span class="del">−{{ fmt.number(r.removed) }}</span>
          </td>
          <td class="num">{{ fmt.number(r.outOfLane) }}</td>
          <td class="num">{{ fmt.compact(r.tokens) }}</td>
          <td class="num">{{ r.durationMs === null ? '–' : fmt.duration(r.durationMs) }}</td>
          <td>{{ r.verifier ?? '–' }}</td>
          <td>
            <IconButton
              icon="git"
              size="sm"
              :label="t('team.contests.showDiff', { label: r.label })"
              :pressed="selected === r.label"
              @click="emit('select', r.label)"
            />
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.board {
  overflow-x: auto;
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
}

.board__table {
  min-width: 100%;
  border-collapse: collapse;
  font-size: var(--fs-sm);
}

.board__table th,
.board__table td {
  padding: var(--space-2) var(--space-3);
  border-bottom: var(--border-width) solid var(--border-subtle);
  text-align: left;
  white-space: nowrap;
}

.board__table thead th {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.board__table tbody tr:last-child > * {
  border-bottom: 0;
}

.board__table .num {
  text-align: right;
  font-variant-numeric: tabular-nums;
}

.is-out {
  color: var(--text-muted);
}

.is-selected {
  background: var(--bg-active);
}

.board__label {
  font-weight: var(--fw-semibold);
}

.board__mark,
.board__out {
  margin-left: var(--space-2);
  padding: 0 var(--space-2);
  border-radius: var(--radius-pill);
  background: var(--tone-bg);
  color: var(--tone-fg);
  font-size: var(--fs-2xs);
  font-weight: var(--fw-medium);
}

.board__out {
  margin-left: 0;
}

.add {
  margin-right: var(--space-1);
  color: var(--diff-add);
}

.del {
  color: var(--diff-del);
}
</style>
