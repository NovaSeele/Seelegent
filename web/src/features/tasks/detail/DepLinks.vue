<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { useTasksStore } from '@/stores/tasks'

/**
 * "Runs after" and the earlier tasks, each by its title with its id as secondary text (the id alone when the task
 * is not loaded).
 */
const props = defineProps<{ ids: readonly string[] }>()

const { t } = useI18n()
const tasks = useTasksStore()

const deps = computed(() => props.ids.map((id) => ({ id, title: tasks.get(id)?.title ?? '' })))
</script>

<template>
  <p v-if="deps.length" class="deps">
    {{ t('task.header.after') }}
    <RouterLink
      v-for="dep in deps"
      :key="dep.id"
      :to="{ name: 'tasks', params: { taskId: dep.id } }"
      :title="dep.title ? dep.id : undefined"
      class="deps__link"
    >
      <span v-if="dep.title" class="deps__title">{{ dep.title }}</span>
      <span class="deps__id">{{ dep.id }}</span>
    </RouterLink>
  </p>
</template>

<style scoped>
.deps {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-1) var(--space-2);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.deps__link {
  display: inline-flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-1);
  min-width: 0;
  color: var(--link);
  overflow-wrap: anywhere;
}

.deps__title + .deps__id {
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: var(--fs-2xs);
}

.deps__id:only-child {
  font-family: var(--font-mono);
}
</style>
