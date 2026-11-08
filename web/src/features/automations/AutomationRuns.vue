<script setup lang="ts">
import { computed, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import type { Automation, AutomationRun } from '@/api/types'
import Dialog from '@/components/ui/Dialog.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Spinner from '@/components/ui/Spinner.vue'
import StatusPill from '@/components/ui/StatusPill.vue'
import { useFormat } from '@/composables/useFormat'
import { useAutomationsStore } from '@/stores/automations'

import { OUTCOME_LOOK } from './automationModel'
import { runView } from './runText'

/** An automation's newest runs: when, why it ran, what came of it (a task, or why it was skipped). */
const props = defineProps<{ automation: Automation | null }>()
const open = defineModel<boolean>('open', { required: true })

const { t } = useI18n()
const fmt = useFormat()
const store = useAutomationsStore()

const runs = shallowRef<readonly AutomationRun[]>([])
const loading = shallowRef(false)
const error = shallowRef('')
const views = computed(() =>
  runs.value.map((r) => ({ run: r, ...runView(r, (k, p) => (p ? t(k, p) : t(k)), fmt.value) })),
)

// Each read gets a number; an answer for an earlier read (another automation, or a dialog closed since) is dropped,
// so a slow answer for A never fills the dialog that now shows B (E-B17).
let request = 0

watch([open, () => props.automation?.id], async ([now, id]) => {
  const mine = ++request
  if (!now || !id) return
  loading.value = true
  error.value = ''
  runs.value = []
  try {
    const list = await store.api().runs(id, 50)
    if (mine === request) runs.value = list
  } catch (cause) {
    if (mine === request) error.value = errorMessage(cause)
  } finally {
    if (mine === request) loading.value = false
  }
})
</script>

<template>
  <Dialog
    :open="open"
    size="lg"
    :title="t('automations.runs.title', { name: automation?.name ?? '' })"
    @update:open="open = $event"
  >
    <Spinner v-if="loading" :label="t('automations.runs.loading')" />
    <p v-else-if="error" class="runs__error" role="alert">{{ error }}</p>
    <EmptyState
      v-else-if="!runs.length"
      icon="automations"
      :level="3"
      :title="t('automations.runs.empty')"
      :description="t('automations.runs.emptyDesc')"
    />
    <ol v-else class="runs">
      <li v-for="{ run: r, why, output, outputLabel } in views" :key="r.id" class="run">
        <StatusPill
          size="sm"
          :tone="OUTCOME_LOOK[r.outcome].tone"
          :icon="OUTCOME_LOOK[r.outcome].icon"
          :label="t(`automations.outcome.${r.outcome}`)"
        />
        <span class="run__meta">
          <span>{{ fmt.dateTime(r.at) }}</span>
          <span>{{ t(`automations.trigger.${r.trigger}`) }}</span>
          <RouterLink v-if="r.taskId" :to="{ name: 'tasks', params: { taskId: r.taskId } }">
            {{ t('automations.runs.task') }}
          </RouterLink>
        </span>
        <p v-if="why" class="run__why">{{ why }}</p>
        <p v-if="output" class="run__why">
          {{ outputLabel }}
          <code class="run__detail">{{ output }}</code>
        </p>
      </li>
    </ol>
  </Dialog>
</template>

<style scoped>
.runs {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.run {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) var(--space-3);
  padding-bottom: var(--space-2);
  border-bottom: var(--border-width) solid var(--border-subtle);
}

.run__meta {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-3);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.run__meta a {
  color: var(--link);
}

@media (pointer: coarse) {
  .run__meta a {
    display: inline-flex;
    align-items: center;
    min-height: var(--target-min);
  }
}

.run__why {
  flex-basis: 100%;
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  overflow-wrap: anywhere;
}

.run__detail {
  display: block;
  margin-top: var(--space-1);
  max-height: 120px;
  overflow: auto;
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.runs__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
}
</style>
