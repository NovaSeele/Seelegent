<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Contest } from '@/api/types'
import IconButton from '@/components/ui/IconButton.vue'
import ProviderMark from '@/components/ui/ProviderMark.vue'
import StatusPill from '@/components/ui/StatusPill.vue'
import type { Tone } from '@/components/ui/tone'
import { useFormat } from '@/composables/useFormat'

import { gateCell, outTone, runsOn, variantStatus, verifierCell } from './contestModel'

/**
 * The machine score of each variant, in the machine's ranking order: gate, auto-fix turns, files, lines, lane,
 * tokens, time and verifier, in the words and tones of the task views. A disqualified variant says why in words.
 * The proposed winner and the picked one are marked. Each row opens the variant's changes.
 */
const props = defineProps<{ contest: Contest; selected: string | null }>()
const emit = defineEmits<{ select: [label: string] }>()

const { t } = useI18n()
const fmt = useFormat()

const rows = computed(() => {
  const c = props.contest
  const rank = new Map(c.machineRanking.map((l, i) => [l, i]))
  return [...c.scoreboard]
    .sort(
      (a, b) =>
        (rank.get(a.label) ?? 99) - (rank.get(b.label) ?? 99) || a.label.localeCompare(b.label),
    )
    .map((r) => {
      const mark: { text: string; tone: Tone } | null =
        r.label === c.winner
          ? { text: t('team.contests.winner'), tone: 'success' }
          : r.label === c.proposal?.winner
            ? { text: t('team.contests.proposal'), tone: 'accent' }
            : null
      const status = variantStatus(r.status)
      return {
        r,
        mark,
        // The backend (and provider) is its ProviderMark; the text is the member and model.
        runsOn: r.backend ? runsOn({ member: r.member, model: r.model }) : runsOn(r),
        out: r.disqualified
          ? t('team.contests.out', { reason: t(`team.contests.disqualified.${r.disqualified}`) })
          : '',
        outTone: outTone(r.disqualified ?? ''),
        status: 'status' in status ? status.status : undefined,
        statusTone: 'tone' in status ? status.tone : undefined,
        statusWord: 'key' in status ? t(status.key) : undefined,
        gate: gateCell(r.gate),
        verifier: verifierCell(r.verifier),
      }
    })
})
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
            <span class="sr-only">{{ t('team.contests.col.diff') }}</span>
          </th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="row in rows"
          :key="row.r.label"
          :class="{ 'is-out': row.r.disqualified, 'is-selected': selected === row.r.label }"
        >
          <th scope="row">
            <span class="board__label">{{ row.r.label }}</span>
            <span v-if="row.mark" class="board__mark" :class="`tone-${row.mark.tone}`">{{
              row.mark.text
            }}</span>
          </th>
          <td>
            <ProviderMark
              v-if="row.r.backend"
              :backend="row.r.backend"
              :provider="row.r.provider"
              size="sm"
            />
            {{ row.runsOn }}
          </td>
          <td>
            <StatusPill v-if="row.out" size="sm" :tone="row.outTone" :label="row.out" />
            <StatusPill v-else-if="row.status" size="sm" :status="row.status" />
            <StatusPill v-else size="sm" :tone="row.statusTone" :label="row.statusWord" />
          </td>
          <td>
            <StatusPill size="sm" :tone="row.gate.tone" :label="t(row.gate.key)" />
          </td>
          <td class="num">{{ fmt.number(row.r.autoFixTurns) }}</td>
          <td class="num">{{ fmt.number(row.r.files) }}</td>
          <td class="num">
            <span class="add">+{{ fmt.number(row.r.added) }}</span>
            <span class="del">−{{ fmt.number(row.r.removed) }}</span>
          </td>
          <td class="num">{{ fmt.number(row.r.outOfLane) }}</td>
          <td class="num">{{ fmt.compact(row.r.tokens) }}</td>
          <td class="num">
            {{ row.r.durationMs === null ? '–' : fmt.duration(row.r.durationMs) }}
          </td>
          <td>
            <StatusPill
              v-if="row.verifier"
              size="sm"
              :tone="row.verifier.tone"
              :label="t(row.verifier.key)"
            />
            <span v-else>–</span>
          </td>
          <td>
            <IconButton
              icon="git"
              size="sm"
              :label="t('team.contests.showDiff', { label: row.r.label })"
              :pressed="selected === row.r.label"
              @click="emit('select', row.r.label)"
            />
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped>
.board {
  /* The table scrolls inside its box; the page never widens for it. Positioned, so the sr-only header text
     (position: absolute) is clipped here too instead of widening the page. */
  position: relative;
  max-width: 100%;
  min-width: 0;
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

/* Selection is a bar at the row's start, not a tint: the +/− counts keep their contrast on the surface. */
.is-selected > th {
  box-shadow: inset 3px 0 0 var(--accent);
}

.board__label {
  font-weight: var(--fw-semibold);
}

.board__mark {
  margin-left: var(--space-2);
  padding: 0 var(--space-2);
  border: var(--border-width) solid var(--tone-border);
  border-radius: var(--radius-pill);
  background: var(--tone-bg);
  /* Body text on the tint: the colour is in the border, the word stays readable (contrast ≥ 4.5). */
  color: var(--text-primary);
  font-size: var(--fs-2xs);
  font-weight: var(--fw-medium);
}

.add {
  margin-right: var(--space-1);
  color: var(--diff-add);
}

.del {
  color: var(--diff-del);
}
</style>
