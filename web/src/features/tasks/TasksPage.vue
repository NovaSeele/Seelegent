<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import SplitLayout from '@/components/layout/SplitLayout.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import { useBreakpoint } from '@/composables/useMediaQuery'
import { useUiStore } from '@/stores/ui'

import TaskDetail from './detail/TaskDetail.vue'
import TaskList from './TaskList.vue'

/**
 * Tasks: the list in the sidebar, the open task beside it. Below 900px one pane shows at a time (the task when
 * one is open, with a back link to the list). The route passes its parameter as a prop (props: true), so it
 * follows the page on screen even while an overlay route (settings) is the current one.
 */
const props = defineProps<{ taskId?: string }>()

const { t } = useI18n()
const ui = useUiStore()
const { isRail } = useBreakpoint()

/** The sidebar carries the page's h1; when it is not on screen the task title takes over as h1. */
const sidebarShown = computed(() => (isRail.value ? !ui.sidebarHidden : !props.taskId))
</script>

<template>
  <SplitLayout :sidebar-label="t('tasks.list.label')" :detail-open="!!taskId">
    <template #sidebar>
      <TaskList :current-id="taskId" />
    </template>
    <TaskDetail
      v-if="taskId"
      :key="taskId"
      :task-id="taskId"
      :title-level="sidebarShown ? 2 : 1"
      :show-back="!isRail"
    />
    <div v-else class="tasks-pick">
      <EmptyState
        icon="tasks"
        :title="t('tasks.detail.pick')"
        :description="t('tasks.detail.pickDesc')"
      />
    </div>
  </SplitLayout>
</template>

<style scoped>
.tasks-pick {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 100%;
}
</style>
