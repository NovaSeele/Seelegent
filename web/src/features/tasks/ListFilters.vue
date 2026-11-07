<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import Button from '@/components/ui/Button.vue'
import Chip from '@/components/ui/Chip.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import Select from '@/components/ui/Select.vue'
import { useConnectionStore } from '@/stores/connection'
import { useTasksStore } from '@/stores/tasks'

import { STATUS_FILTERS, type Grouping, type StatusFilter } from './listModel'

/** Status chips (any of them), a project picker and the grouping switch, under the search box. */
const statuses = defineModel<StatusFilter[]>('statuses', { required: true })
const project = defineModel<string>('project', { required: true })
const grouping = defineModel<Grouping>('grouping', { required: true })

const { t } = useI18n()
const connection = useConnectionStore()
const tasks = useTasksStore()

/** Projects the daemon knows, plus any seen only on a loaded task. */
const projectOptions = computed(() => {
  const names = new Map<string, string>()
  for (const p of connection.projects) names.set(p.key, p.name)
  for (const task of tasks.summaries.values()) {
    if (!names.has(task.projectKey)) names.set(task.projectKey, task.projectName)
  }
  const list = [...names.entries()]
    .sort((a, b) => a[1].localeCompare(b[1]))
    .map(([value, label]) => ({ value, label }))
  return [{ value: '', label: t('tasks.list.allProjects') }, ...list]
})

const groupingOptions = computed(() => [
  { value: 'project' as const, label: t('tasks.list.groupProject') },
  { value: 'flat' as const, label: t('tasks.list.groupFlat') },
])

function toggle(filter: StatusFilter) {
  statuses.value = statuses.value.includes(filter)
    ? statuses.value.filter((f) => f !== filter)
    : [...statuses.value, filter]
}

const active = computed(() => statuses.value.length > 0 || project.value !== '')

function clear() {
  statuses.value = []
  project.value = ''
}
</script>

<template>
  <div class="filters">
    <div class="filters__chips" role="group" :aria-label="t('tasks.list.status')">
      <Chip
        v-for="filter in STATUS_FILTERS"
        :key="filter"
        :selected="statuses.includes(filter)"
        @toggle="toggle(filter)"
      >
        {{ t(`tasks.filter.${filter}`) }}
      </Chip>
    </div>
    <div class="filters__row">
      <Select
        v-model="project"
        class="filters__project"
        :label="t('tasks.list.project')"
        hide-label
        size="sm"
        :options="projectOptions"
      />
      <SegmentedControl
        v-model="grouping"
        :options="groupingOptions"
        :label="t('tasks.list.grouping')"
        size="sm"
      />
    </div>
    <Button v-if="active" size="sm" variant="ghost" icon="x" @click="clear">
      {{ t('tasks.list.clearFilters') }}
    </Button>
  </div>
</template>

<style scoped>
.filters {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--space-2);
}

.filters__chips {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.filters__row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
}

.filters__project {
  flex: 1 1 140px;
  min-width: 0;
}
</style>
