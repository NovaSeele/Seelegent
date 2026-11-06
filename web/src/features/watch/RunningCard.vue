<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import StatusPill from '@/components/ui/StatusPill.vue'
import { useFormat } from '@/composables/useFormat'
import { displayStatus, statusWordKey } from '@/lib/taskStatus'
import { useTasksStore } from '@/stores/tasks'

/** A task in progress: what it is doing now, for how long, and on which account. */
const props = defineProps<{ id: string; now: number }>()

const { t } = useI18n()
const fmt = useFormat()
const tasks = useTasksStore()

const task = computed(() => tasks.get(props.id))
const status = computed(() => (task.value ? displayStatus(task.value) : 'queued'))
const elapsed = computed(() =>
  task.value ? Math.max(0, props.now - Date.parse(task.value.createdAt)) : 0,
)
const stepFor = computed(() => {
  const since = task.value?.activeStep?.since
  return since ? fmt.value.duration(Math.max(0, props.now - Date.parse(since))) : ''
})
const until = computed(() =>
  task.value?.waitingUntil ? fmt.value.relative(task.value.waitingUntil, props.now) : '',
)
</script>

<template>
  <article v-if="task" class="run" :data-status="task.status">
    <header class="run__head">
      <StatusPill :status="status" :label="t(statusWordKey(task))" size="sm" />
      <span class="run__elapsed">
        <time :datetime="task.createdAt" :title="fmt.dateTime(task.createdAt)">
          {{ t('watch.running.elapsed', { time: fmt.duration(elapsed) }) }}
        </time>
      </span>
    </header>
    <h3 class="run__title">
      <RouterLink :to="{ name: 'tasks', params: { taskId: id } }" class="run__link">
        {{ task.title }}
      </RouterLink>
    </h3>
    <p class="run__meta">
      {{ task.projectName }}
      <template v-if="task.accountLabel"> · {{ task.accountLabel }}</template>
      <template v-if="task.model"> · {{ task.model }}</template>
    </p>
    <p v-if="task.status === 'running'" class="run__step" aria-live="off">
      <span class="run__step-label">{{ task.activeStep?.label ?? t('watch.running.noStep') }}</span>
      <span v-if="stepFor" class="run__step-time">{{ stepFor }}</span>
    </p>
    <p v-else-if="until" class="run__step">{{ t('watch.running.until', { time: until }) }}</p>
    <p v-else-if="task.depsReason" class="run__step">{{ task.depsReason }}</p>
    <footer class="run__foot">
      <span v-if="task.turnCount">{{ t('watch.running.turn', { n: task.turnCount }) }}</span>
      <span v-if="task.tokensUsed">
        {{ t('watch.running.tokens', { n: fmt.compact(task.tokensUsed) }) }}
      </span>
      <span v-if="task.filesChanged">
        {{ t('watch.running.files', task.filesChanged) }}
      </span>
    </footer>
  </article>
</template>

<style scoped>
.run {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
  padding: var(--space-3) var(--space-4);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
}

.run:focus-within {
  border-color: var(--border-strong);
}

.run__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.run__elapsed {
  color: var(--text-muted);
  font-size: var(--fs-xs);
  font-variant-numeric: tabular-nums;
}

.run__title {
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
  line-height: var(--lh-tight);
}

.run__link {
  color: var(--text-primary);
  text-decoration: none;
  overflow-wrap: anywhere;
}

.run__link::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: inherit;
}

.run__link:hover {
  text-decoration: underline;
}

.run__link:focus-visible {
  outline: none;
}

.run__link:focus-visible::after {
  box-shadow: var(--focus-ring);
}

.run__meta {
  color: var(--text-muted);
  font-size: var(--fs-xs);
  overflow-wrap: anywhere;
}

.run__step {
  display: flex;
  gap: var(--space-2);
  min-width: 0;
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm);
  background: var(--bg-surface-2);
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
}

.run__step-label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.run__step-time {
  flex: none;
  color: var(--text-muted);
  font-variant-numeric: tabular-nums;
}

.run__foot {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-3);
  color: var(--text-muted);
  font-size: var(--fs-xs);
  font-variant-numeric: tabular-nums;
}
</style>
