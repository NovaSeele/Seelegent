<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'

import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import Button from '@/components/ui/Button.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Spinner from '@/components/ui/Spinner.vue'
import StatusPill from '@/components/ui/StatusPill.vue'
import { useFormat } from '@/composables/useFormat'
import { CONTEST_STATUS } from '@/features/meetings/meetingModel'
import { useTeamStore } from '@/stores/team'

import ContestDetail from './ContestDetail.vue'

/**
 * Contests: the list (newest first, live over contest.updated) or, with a contest id, that contest. The lead
 * starts contests (delegate with variants); the dashboard follows, compares and picks.
 */
const props = defineProps<{ contestId?: string }>()

const { t } = useI18n()
const fmt = useFormat()
const team = useTeamStore()

onMounted(() => team.ensureContests())

const shown = computed(() =>
  [...team.contests].sort((a, b) => b.createdAt.localeCompare(a.createdAt)),
)
const look = (status: keyof typeof CONTEST_STATUS) => CONTEST_STATUS[status]
</script>

<template>
  <PageContainer>
    <ContestDetail v-if="props.contestId" :key="props.contestId" :contest-id="props.contestId" />
    <template v-else>
      <PageHeader :title="t('nav.contests.label')" :description="t('nav.contests.desc')" />
      <div class="pad">
        <Spinner v-if="team.contestsStatus === 'loading'" :label="t('team.contests.loading')" />
        <div v-else-if="team.contestsStatus === 'error'" class="note" role="alert">
          {{ t('team.contests.loadFailed', { error: team.contestsError }) }}
          <Button size="sm" variant="ghost" @click="team.loadContests()">
            {{ t('common.action.retry') }}
          </Button>
        </div>
        <EmptyState
          v-else-if="!shown.length"
          icon="contests"
          :title="t('team.contests.empty')"
          :description="t('team.contests.emptyDesc')"
        />
        <ul v-else class="list">
          <li v-for="c in shown" :key="c.id">
            <RouterLink class="row" :to="{ name: 'contests', params: { contestId: c.id } }">
              <span class="row__main">
                <span class="row__title">{{ c.title }}</span>
                <span class="row__meta">
                  <span>{{ t('team.contests.variantsN', { n: c.variants }, c.variants) }}</span>
                  <span v-if="c.winner">{{ t('team.contests.winner') }}: {{ c.winner }}</span>
                  <span v-else-if="c.proposal"
                    >{{ t('team.contests.proposal') }}: {{ c.proposal }}</span
                  >
                  <span>{{ fmt.dateTime(c.createdAt) }}</span>
                </span>
              </span>
              <StatusPill
                size="sm"
                :tone="look(c.status).tone"
                :icon="look(c.status).icon"
                :label="t(`team.contests.status.${c.status}`)"
              />
            </RouterLink>
          </li>
        </ul>
      </div>
    </template>
  </PageContainer>
</template>

<style scoped>
.pad {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  padding: 0 var(--space-6) var(--space-7);
}

@media (width < 600px) {
  .pad {
    padding-inline: var(--space-4);
  }
}

.note {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
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
</style>
