<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'

import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import Button from '@/components/ui/Button.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import IconButton from '@/components/ui/IconButton.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useFormat } from '@/composables/useFormat'
import { useConnectionStore } from '@/stores/connection'
import { useUsageStore, type UsageRange } from '@/stores/usage'

import DailyChart from './DailyChart.vue'
import QuotaHistory from './QuotaHistory.vue'
import UsageAccounts from './UsageAccounts.vue'
import UsageProjects from './UsageProjects.vue'
import UsageTotals from './UsageTotals.vue'

/**
 * Usage over the last 7 or 30 days (the choice is remembered): totals against the period before, tokens per day,
 * the projects and accounts that used the most, and each account's quota over time. Read when the page opens,
 * when the range changes, on refresh and after a reconnect.
 */
const { t } = useI18n()
const fmt = useFormat()
const usage = useUsageStore()
const connection = useConnectionStore()

onMounted(() => usage.ensure())

const ranges = computed(() =>
  ([7, 30] as const).map((value) => ({ value, label: t('usage.range', { n: value }) })),
)
const range = computed<UsageRange>({
  get: () => usage.days,
  set: (value) => (usage.days = value),
})

const period = computed(() => {
  const s = usage.summary
  if (!s) return undefined
  return t('usage.period', { from: fmt.value.date(s.since), to: fmt.value.date(s.until) })
})
</script>

<template>
  <PageContainer>
    <PageHeader :title="t('nav.usage.label')" :description="period ?? t('nav.usage.desc')">
      <template #actions>
        <SegmentedControl
          v-model="range"
          :options="ranges"
          :label="t('usage.rangeLabel')"
          size="sm"
        />
        <IconButton
          icon="refresh"
          size="sm"
          variant="secondary"
          :label="t('usage.refresh')"
          :disabled="usage.busy"
          @click="usage.load()"
        />
      </template>
    </PageHeader>

    <div class="usage" :aria-busy="usage.busy">
      <div v-if="usage.summaryStatus === 'loading'" class="usage__loading">
        <Spinner :label="t('usage.loading')" />
      </div>
      <EmptyState
        v-else-if="usage.summaryStatus === 'error'"
        icon="warning"
        :title="t('usage.loadFailed')"
        :description="usage.summaryError ?? undefined"
      >
        <Button variant="secondary" @click="usage.load()">{{ t('common.action.retry') }}</Button>
      </EmptyState>
      <template v-else-if="usage.summary">
        <p v-if="usage.summaryError" class="usage__stale" role="status">
          {{ t('usage.stale', { error: usage.summaryError }) }}
        </p>
        <UsageTotals :summary="usage.summary" />
        <p class="usage__note">{{ t('usage.tokensNote') }}</p>
        <DailyChart :daily="usage.summary.daily" :has-cost="usage.summary.totals.hasCost" />
        <div class="usage__pair">
          <UsageProjects :summary="usage.summary" />
          <UsageAccounts :summary="usage.summary" />
        </div>
      </template>

      <QuotaHistory
        v-if="usage.history"
        :history="usage.history"
        :min-remaining="connection.settings?.minRemaining ?? 0"
      />
      <p v-else-if="usage.historyStatus === 'error'" class="usage__stale" role="alert">
        {{ t('usage.historyFailed', { error: usage.historyError }) }}
      </p>
      <p v-if="usage.summary?.dataSince" class="usage__note">
        {{ t('usage.dataSince', { date: fmt.date(usage.summary.dataSince) }) }}
      </p>
    </div>
  </PageContainer>
</template>

<style scoped>
.usage {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  padding: 0 var(--space-6) var(--space-7);
}

.usage__loading {
  display: flex;
  justify-content: center;
  padding: var(--space-8) 0;
}

.usage__pair {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 460px), 1fr));
  gap: var(--space-4);
}

.usage__stale,
.usage__note {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

@media (width < 600px) {
  .usage {
    padding: 0 var(--space-4) var(--space-6);
  }
}
</style>
