<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import type { ProofMatrix } from '@/api/types'
import Chip from '@/components/ui/Chip.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useFormat } from '@/composables/useFormat'

import ProofCounts from './ProofCounts.vue'
import { statusTone } from './proofModel'

/**
 * The requirement matrix of one feature: each FR and SC with its status and the spec tasks that cover it, with
 * their last gate result (a worker's gate links to the task). Stories and tasks without Covers: follow.
 */
defineProps<{ matrix: ProofMatrix | null; loading: boolean; error: string }>()

const { t } = useI18n()
const fmt = useFormat()

const resultTone = (state: string | undefined) =>
  state === 'green' ? 'success' : state === 'red' || state === 'error' ? 'danger' : 'neutral'
</script>

<template>
  <section class="matrix" :aria-busy="loading">
    <Spinner v-if="loading && !matrix" :label="t('proof.matrix.loading')" />
    <p v-else-if="error" class="matrix__error" role="alert">{{ error }}</p>
    <p v-else-if="!matrix" class="matrix__muted">{{ t('proof.matrix.pick') }}</p>
    <template v-else>
      <header class="matrix__head">
        <h2 class="matrix__title">{{ matrix.title || matrix.feature }}</h2>
        <span class="matrix__muted">
          {{ t('proof.features.tasks', { done: matrix.tasks.done, total: matrix.tasks.total }) }}
        </span>
      </header>
      <ProofCounts :counts="matrix.counts" />
      <p v-if="matrix.featureGate.length" class="matrix__muted">
        {{ t('proof.matrix.featureGate') }}
        <code v-for="g in matrix.featureGate" :key="g" class="matrix__code">{{ g }}</code>
      </p>
      <ul class="matrix__rows">
        <li v-for="row in matrix.rows" :key="row.id" class="matrix__row">
          <div class="matrix__line">
            <code class="matrix__id">{{ row.id }}</code>
            <Chip :tone="statusTone(row.status)">{{ t(`proof.status.${row.status}`) }}</Chip>
            <span class="matrix__text">{{ row.text }}</span>
          </div>
          <ul v-if="row.tasks.length" class="matrix__tasks">
            <li v-for="task in row.tasks" :key="task.id" class="matrix__task">
              <span class="matrix__tid">{{ task.id }}</span>
              <span class="matrix__muted">
                {{ task.done ? t('proof.matrix.done') : t('proof.matrix.open') }}
              </span>
              <Chip v-if="task.state" :tone="resultTone(task.state)">
                {{ t(`proof.matrix.result.${task.state}`) }}
              </Chip>
              <span v-else class="matrix__muted">
                {{ task.gates.length ? t('proof.matrix.noResult') : t('proof.matrix.noGate') }}
              </span>
              <RouterLink
                v-if="task.ref && task.source === 'task'"
                :to="{ name: 'tasks', params: { taskId: task.ref } }"
                class="matrix__link"
              >
                {{ task.ref }}
              </RouterLink>
              <span v-else-if="task.ref" class="matrix__muted">
                {{ t('proof.matrix.byRun', { id: task.ref }) }}
              </span>
              <time v-if="task.at" class="matrix__muted" :datetime="task.at">
                {{ fmt.dateTime(task.at) }}
              </time>
            </li>
          </ul>
        </li>
      </ul>
      <section v-if="matrix.stories.length" class="matrix__stories">
        <h3 class="matrix__sub">{{ t('proof.matrix.stories') }}</h3>
        <div v-for="s in matrix.stories" :key="s.id" class="matrix__story">
          <span class="matrix__story-name">{{ s.id }} · {{ s.title }} ({{ s.priority }})</span>
          <ProofCounts :counts="s.counts" compact />
        </div>
      </section>
      <p v-if="matrix.uncoveredTasks.length" class="matrix__muted">
        {{ t('proof.matrix.uncovered', { tasks: matrix.uncoveredTasks.join(', ') }) }}
      </p>
    </template>
  </section>
</template>

<style scoped>
.matrix {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  min-width: 0;
}

.matrix__head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-2);
}

.matrix__title {
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
  overflow-wrap: anywhere;
}

.matrix__sub {
  font-size: var(--fs-sm);
  font-weight: var(--fw-semibold);
}

.matrix__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
}

.matrix__muted {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  overflow-wrap: anywhere;
}

.matrix__code,
.matrix__id {
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
}

.matrix__code {
  margin-left: var(--space-1);
}

.matrix__rows,
.matrix__tasks {
  display: flex;
  flex-direction: column;
  margin: 0;
  padding: 0;
  list-style: none;
}

.matrix__rows {
  gap: var(--space-2);
}

.matrix__row {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding: var(--space-2) var(--space-3);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
}

.matrix__line,
.matrix__task {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) var(--space-2);
  font-size: var(--fs-sm);
}

.matrix__text {
  flex: 1 1 16rem;
  min-width: 0;
  overflow-wrap: anywhere;
}

.matrix__tasks {
  gap: var(--space-1);
  padding-left: var(--space-4);
}

.matrix__tid {
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.matrix__link {
  color: var(--accent);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
}

.matrix__stories {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.matrix__story {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  font-size: var(--fs-sm);
}

@media (pointer: coarse) {
  .matrix__link {
    display: inline-flex;
    align-items: center;
    min-height: var(--target-min);
  }
}
</style>
