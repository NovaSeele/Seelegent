<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type { TaskDetail, TaskSummary } from '@/api/types'
import EmptyState from '@/components/ui/EmptyState.vue'

import { VIEW_ICONS, type TaskView } from './taskKeys'

/** The panel of the selected task view. */
defineProps<{ view: TaskView; task: TaskSummary; detail: TaskDetail | null }>()

const { t } = useI18n()
</script>

<template>
  <EmptyState
    :icon="VIEW_ICONS[view]"
    :level="3"
    :title="t(`task.views.${view}`)"
    :description="t('pages.placeholder.desc')"
  />
</template>
