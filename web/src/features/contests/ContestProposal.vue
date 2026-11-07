<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Contest } from '@/api/types'
import ClaimList from '@/features/meetings/ClaimList.vue'
import { memberLabel } from '@/features/members/memberModel'
import { useTeamStore } from '@/stores/team'

import { letterLegend } from './contestModel'

/**
 * The winner Seelegent proposes and why: the judge's pick when it is qualified and backed by a verified citation,
 * else the machine score's; and the judge's ranking with its reasons (each citation checked).
 */
const props = defineProps<{ contest: Contest }>()

const { t } = useI18n()
const team = useTeamStore()
onMounted(() => team.ensureMembers())

/** Why the machine's winner is proposed, in the reader's language (the daemon's English only as a fallback). */
const reason = computed(() => {
  const p = props.contest.proposal
  if (!p) return ''
  return p.reasonCode ? t(`team.contests.reasonCode.${p.reasonCode}`) : (p.reason ?? '')
})
const legend = computed(() => letterLegend(props.contest.judge?.letters ?? {}))
const judgeName = computed(() => memberLabel(team.members, props.contest.judge?.member ?? ''))
</script>

<template>
  <section v-if="contest.proposal || contest.judge" class="proposal">
    <h2 class="proposal__h">{{ t('team.contests.proposal') }}</h2>
    <p v-if="contest.proposal">
      <strong>
        {{
          t('team.contests.proposed', {
            winner: contest.proposal.winner,
            source: t(`team.contests.source.${contest.proposal.source}`),
          })
        }}
      </strong>
      <span v-if="reason" class="proposal__muted">
        {{ t('team.contests.because', { reason }) }}
      </span>
    </p>
    <p
      v-if="
        contest.proposal?.machineWinner &&
        contest.proposal.machineWinner !== contest.proposal.winner
      "
      class="proposal__muted"
    >
      {{ t('team.contests.machineWinner', { winner: contest.proposal.machineWinner }) }}
    </p>
    <template v-if="contest.judge">
      <h3 class="proposal__sub">
        {{ t('team.contests.judge', { member: judgeName }) }}
      </h3>
      <p v-if="legend.length" class="proposal__muted">
        {{ t('team.contests.letters', { list: legend.join(' · ') }) }}
      </p>
      <p v-if="contest.judge.status === 'running'" class="proposal__muted">
        {{ t('team.contests.judgeRunning') }}
      </p>
      <p v-else-if="contest.judge.status === 'failed'" class="proposal__error">
        {{ t('team.contests.judgeFailed', { error: contest.judge.error ?? '' }) }}
      </p>
      <template v-else>
        <p v-if="contest.judge.rankingLabels.length">
          {{ t('team.contests.ranking', { list: contest.judge.rankingLabels.join(' > ') }) }}
          <span class="proposal__muted">
            ({{
              t('team.contests.verifiedN', { n: contest.judge.verified }, contest.judge.verified)
            }})
          </span>
        </p>
        <ClaimList :claims="contest.judge.reasons" />
      </template>
    </template>
  </section>
</template>

<style scoped>
.proposal {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-4);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
  font-size: var(--fs-sm);
}

.proposal__h {
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
}

.proposal__sub {
  margin-top: var(--space-2);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.proposal__muted {
  color: var(--text-secondary);
}

.proposal__error {
  color: var(--status-danger-fg);
}
</style>
