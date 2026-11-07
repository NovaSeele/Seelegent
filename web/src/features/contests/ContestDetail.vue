<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import type { Contest } from '@/api/types'
import PageHeader from '@/components/layout/PageHeader.vue'
import Button from '@/components/ui/Button.vue'
import ConfirmDialog from '@/components/ui/ConfirmDialog.vue'
import Spinner from '@/components/ui/Spinner.vue'
import StatusPill from '@/components/ui/StatusPill.vue'
import { useFormat } from '@/composables/useFormat'
import { CONTEST_STATUS, contestOpen } from '@/features/meetings/meetingModel'
import { useLiveRecord } from '@/features/meetings/useLiveRecord'
import { truncateMiddle } from '@/lib/paths'
import { useTeamStore } from '@/stores/team'
import { useToastStore } from '@/stores/toast'

import ContestDiffView from './ContestDiffView.vue'
import ContestPick from './ContestPick.vue'
import ContestProposal from './ContestProposal.vue'
import ContestScoreboard from './ContestScoreboard.vue'
import ContestVariants from './ContestVariants.vue'

/**
 * One contest: while it runs, one row per variant; then the scoreboard, the judge's verdict with its checked
 * citations, the proposal, and the pick (ContestPick). A pick applies the winner's changes to the user's working
 * tree (never committed); nothing changes when it does not apply, and the error stays on the page. Live over
 * contest.updated for this id.
 */
const props = defineProps<{ contestId: string }>()

const { t } = useI18n()
const fmt = useFormat()
const team = useTeamStore()
const toast = useToastStore()

const { record, status, error, reload, set } = useLiveRecord<Contest>(
  () => props.contestId,
  (id, signal) => team.api().contest(id, signal),
  'contest.updated',
)
const selected = ref<string | null>(null)
const cancelling = ref(false)
const busy = ref(false)
const actionError = ref('')

const look = computed(() => (record.value ? CONTEST_STATUS[record.value.status] : null))

// The changes shown first are the proposed winner's (then the picked one's).
watch(
  () => record.value?.winner ?? record.value?.proposal?.winner,
  (label) => {
    if (label && !selected.value) selected.value = label
  },
  { immediate: true },
)

function onPicked(next: Contest) {
  actionError.value = ''
  set(next)
}

async function cancel() {
  busy.value = true
  actionError.value = ''
  try {
    set(await team.api().cancelContest(props.contestId))
    toast.show({ message: t('team.contests.cancelled'), tone: 'success' })
    cancelling.value = false
  } catch (e) {
    actionError.value = errorMessage(e)
    void reload()
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="contest">
    <div class="pad pad--top">
      <Button
        size="sm"
        variant="ghost"
        icon="arrowLeft"
        class="contest__back"
        :to="{ name: 'contests' }"
      >
        {{ t('team.contests.back') }}
      </Button>
      <div v-if="status === 'error' && !record" class="contest__note" role="alert">
        {{ t('team.contests.notFound', { error }) }}
        <Button size="sm" variant="ghost" @click="reload()">{{ t('common.action.retry') }}</Button>
      </div>
      <Spinner v-else-if="!record" :label="t('team.contests.loading')" />
    </div>
    <template v-if="record">
      <PageHeader
        :title="record.title"
        :description="truncateMiddle(record.cwd)"
        :description-title="record.cwd"
      >
        <template #actions>
          <ContestPick :contest="record" @picked="onPicked" />
          <Button v-if="contestOpen(record.status)" variant="ghost" @click="cancelling = true">
            {{ t('team.contests.cancel') }}
          </Button>
        </template>
      </PageHeader>
      <div class="pad">
        <div class="contest__meta">
          <StatusPill
            v-if="look"
            :tone="look.tone"
            :icon="look.icon"
            :label="t(`team.contests.status.${record.status}`)"
          />
          <span v-if="record.applied">
            {{
              t('team.contests.applied', {
                winner: record.winner ?? '',
                time: fmt.dateTime(record.applied.at),
              })
            }}
          </span>
          <span v-else>{{
            t('team.contests.variantsN', { n: record.variants.length }, record.variants.length)
          }}</span>
          <span v-if="record.deadlineAt && contestOpen(record.status)">
            {{ t('team.contests.deadline', { time: fmt.dateTime(record.deadlineAt) }) }}
          </span>
          <span v-if="record.deadlineHit">{{ t('team.contests.deadlineHit') }}</span>
        </div>
        <p v-if="record.error" class="contest__error" role="alert">{{ record.error }}</p>
        <p
          v-if="record.lastPickError && record.status !== 'applied'"
          class="contest__error"
          role="alert"
        >
          {{
            t('team.contests.pickError', {
              winner: record.lastPickError.winner,
              detail: record.lastPickError.detail,
            })
          }}
        </p>
        <p v-if="actionError" class="contest__error" role="alert">{{ actionError }}</p>

        <ContestVariants v-if="!record.scoreboard.length" :variants="record.variants" />

        <ContestProposal :contest="record" />

        <ContestScoreboard
          v-if="record.scoreboard.length"
          :contest="record"
          :selected="selected"
          @select="selected = $event"
        />
        <ContestDiffView
          v-if="selected && record.scoreboard.length"
          :contest-id="record.id"
          :label="selected"
          :version="record.scoredAt ?? record.updatedAt"
        />

        <section v-if="record.warnings.length" class="contest__block">
          <h2 class="contest__h">{{ t('team.contests.warnings') }}</h2>
          <ul class="contest__list">
            <li v-for="(w, i) in record.warnings" :key="i">{{ w }}</li>
          </ul>
        </section>

        <details class="contest__block">
          <summary class="contest__summary">{{ t('team.contests.brief') }}</summary>
          <p class="contest__text">{{ record.prompt }}</p>
          <ContestVariants v-if="record.scoreboard.length" :variants="record.variants" />
        </details>
      </div>
    </template>

    <ConfirmDialog
      :open="cancelling"
      :title="t('team.contests.cancelTitle')"
      :description="t('team.contests.cancelDesc')"
      :confirm-label="t('team.contests.cancel')"
      danger
      :busy="busy"
      :error="actionError"
      @update:open="cancelling = false"
      @confirm="cancel"
    />
  </div>
</template>

<style scoped>
.contest {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.pad {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  min-width: 0;
  padding: 0 var(--space-6) var(--space-7);
}

/* Each block keeps to the page's width; wide content (the scoreboard, a diff) scrolls inside itself. */
.pad > * {
  min-width: 0;
  max-width: 100%;
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

.contest__back {
  align-self: flex-start;
}

.contest__note,
.contest__meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-4);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.contest__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
  overflow-wrap: anywhere;
}

.contest__block {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-4);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
  font-size: var(--fs-sm);
}

.contest__h {
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
}

.contest__summary {
  min-height: max(32px, var(--target-min));
  font-weight: var(--fw-semibold);
  cursor: pointer;
}

.contest__summary:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}

.contest__text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.contest__list {
  margin: 0;
  padding-left: var(--space-5);
}
</style>
