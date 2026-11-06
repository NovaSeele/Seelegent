<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import Button from '@/components/ui/Button.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useNow } from '@/composables/useNow'
import { stableComputed } from '@/lib/stable'
import { readStored, writeStored } from '@/lib/storage'
import { useTasksStore } from '@/stores/tasks'

import AccountStrip from './AccountStrip.vue'
import NeedCard from './NeedCard.vue'
import RecentRow from './RecentRow.vue'
import RunningCard from './RunningCard.vue'
import { buildBoard, sameBoard, WATCH_TAB, type WatchTab } from './watchModel'

/**
 * Home: "who needs me right now". Tasks that wait for you come first (questions, blocks, stalls, failures,
 * reviews), then what is running, then what finished recently, with the accounts' state and quota below.
 * Everything comes from the tasks store (BootState carries every pending, attention and unreviewed task), so
 * the board fills on the first /state and stays live over SSE without fetching per card.
 */
const { t } = useI18n()
const tasks = useTasksStore()
const now = useNow(1000)
const minute = useNow(30_000)

const tab = ref<WatchTab>(readStored(WATCH_TAB))
watch(tab, (value) => writeStored(WATCH_TAB, value))

// The minute clock only moves failed tasks off the board after a day; the board itself changes with the tasks.
const board = stableComputed(() => buildBoard(tasks.summaries.values(), minute.value), sameBoard)

const tabs = computed(() => [
  { value: 'all' as const, label: t('watch.tabs.all') },
  { value: 'needs' as const, label: t('watch.tabs.needs', { count: board.value.needs.length }) },
  {
    value: 'running' as const,
    label: t('watch.tabs.running', { count: board.value.running.length }),
  },
])

const summary = computed(() => {
  const { needs, running, queued } = board.value
  if (!needs.length && !running.length) return t('watch.summary.allClear')
  const parts = [t('watch.summary.needs', needs.length)]
  const active = running.length - queued
  if (active) parts.push(t('watch.summary.running', active))
  if (queued) parts.push(t('watch.summary.queued', queued))
  return parts.join(' · ')
})

const nothing = computed(
  () => !board.value.needs.length && !board.value.running.length && !board.value.recent.length,
)
const showNeeds = computed(() => tab.value !== 'running' && !(tab.value === 'all' && nothing.value))
const showRunning = computed(() => tab.value !== 'needs' && !(tab.value === 'all' && nothing.value))
const showRest = computed(() => tab.value === 'all')
</script>

<template>
  <PageContainer>
    <PageHeader :title="t('nav.watch.label')" :description="tasks.loaded ? summary : undefined">
      <template #actions>
        <SegmentedControl v-model="tab" :options="tabs" :label="t('watch.tabs.label')" size="sm" />
      </template>
    </PageHeader>

    <div v-if="!tasks.loaded" class="watch__loading">
      <Spinner :label="t('watch.loading')" />
    </div>

    <div v-else class="watch">
      <EmptyState
        v-if="tab === 'all' && nothing"
        icon="watch"
        :title="t('watch.empty.title')"
        :description="t('watch.empty.desc')"
      />
      <section v-if="showNeeds" class="watch__section" aria-labelledby="watch-needs">
        <h2 id="watch-needs" class="watch__heading">
          {{ t('watch.needs.title') }}
          <span v-if="board.needs.length" class="watch__count">{{ board.needs.length }}</span>
        </h2>
        <p v-if="!board.needs.length" class="watch__empty">{{ t('watch.needs.empty') }}</p>
        <div v-else class="watch__grid">
          <NeedCard
            v-for="need in board.needs"
            :id="need.id"
            :key="need.id"
            :kind="need.kind"
            :since="need.since"
            :now="now"
          />
        </div>
      </section>

      <section v-if="showRunning" class="watch__section" aria-labelledby="watch-running">
        <h2 id="watch-running" class="watch__heading">
          {{ t('watch.running.title') }}
          <span v-if="board.running.length" class="watch__count">{{ board.running.length }}</span>
        </h2>
        <p v-if="!board.running.length" class="watch__empty">{{ t('watch.running.empty') }}</p>
        <div v-else class="watch__grid">
          <RunningCard v-for="id in board.running" :id="id" :key="id" :now="now" />
        </div>
      </section>

      <template v-if="showRest">
        <section v-if="!nothing" class="watch__section" aria-labelledby="watch-recent">
          <div class="watch__row">
            <h2 id="watch-recent" class="watch__heading">{{ t('watch.recent.title') }}</h2>
            <Button size="sm" variant="ghost" icon-end="chevronRight" to="/tasks">
              {{ t('watch.recent.all') }}
            </Button>
          </div>
          <p v-if="!board.recent.length" class="watch__empty">{{ t('watch.recent.empty') }}</p>
          <ul v-else class="watch__recent">
            <RecentRow v-for="id in board.recent" :id="id" :key="id" :now="minute" />
          </ul>
        </section>

        <AccountStrip class="watch__section" :now="minute" />
      </template>
    </div>
  </PageContainer>
</template>

<style scoped>
.watch {
  display: flex;
  flex-direction: column;
  gap: var(--space-6);
  padding: 0 var(--space-6) var(--space-7);
}

.watch__loading {
  display: flex;
  justify-content: center;
  padding: var(--space-8) 0;
}

.watch__section {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.watch__row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
}

.watch__heading {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
}

.watch__count {
  min-width: 20px;
  padding: 0 var(--space-1);
  border-radius: var(--radius-pill);
  background: var(--neutral-bg);
  color: var(--neutral-fg);
  font-size: var(--fs-2xs);
  font-variant-numeric: tabular-nums;
  line-height: 20px;
  text-align: center;
}

.watch__empty {
  color: var(--text-muted);
  font-size: var(--fs-sm);
}

.watch__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: var(--space-3);
}

.watch__recent {
  display: flex;
  flex-direction: column;
  list-style: none;
}

@media (width < 600px) {
  .watch {
    gap: var(--space-5);
    padding: 0 var(--space-4) var(--space-6);
  }

  .watch__grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
