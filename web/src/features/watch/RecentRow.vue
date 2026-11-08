<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import StatusPill from '@/components/ui/StatusPill.vue'
import { useFormat } from '@/composables/useFormat'
import { displayStatus } from '@/lib/taskStatus'
import { useTasksStore } from '@/stores/tasks'

/** A finished task in the "recently finished" list. */
const props = defineProps<{ id: string; now: number }>()

const { t } = useI18n()
const fmt = useFormat()
const tasks = useTasksStore()
const task = computed(() => tasks.get(props.id))
</script>

<template>
  <li v-if="task" class="recent">
    <StatusPill :status="displayStatus(task)" size="sm" short class="recent__status" />
    <RouterLink :to="{ name: 'tasks', params: { taskId: id } }" class="recent__title">
      {{ task.title }}
    </RouterLink>
    <span class="recent__project">{{ task.projectName }}</span>
    <time class="recent__time" :datetime="task.updatedAt" :title="fmt.dateTime(task.updatedAt)">
      {{ fmt.ago(task.updatedAt, now, t('common.time.justNow')) }}
    </time>
  </li>
</template>

<style scoped>
.recent {
  display: grid;
  grid-template-columns: 96px minmax(0, 1fr) auto auto;
  align-items: center;
  gap: var(--space-3);
  min-height: var(--control-lg);
  padding: var(--space-1) var(--space-3);
  border-radius: var(--radius-md);
}

.recent:hover {
  background: var(--bg-hover);
}

.recent__status {
  justify-self: start;
}

.recent__title {
  overflow: hidden;
  color: var(--text-primary);
  font-size: var(--fs-sm);
  text-decoration: none;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.recent__title:hover {
  text-decoration: underline;
}

.recent__title:focus-visible {
  outline: none;
  border-radius: var(--radius-xs);
  box-shadow: var(--focus-ring);
}

.recent__project,
.recent__time {
  color: var(--text-muted);
  font-size: var(--fs-xs);
  white-space: nowrap;
}

@media (width < 600px) {
  .recent {
    grid-template-columns: auto minmax(0, 1fr) auto;
  }

  .recent__project {
    display: none;
  }
}
</style>
