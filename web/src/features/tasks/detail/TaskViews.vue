<script setup lang="ts">
import type { TaskDetail, TaskSummary } from '@/api/types'

import GitView from '../views/git/GitView.vue'
import FilesView from '../views/process/FilesView.vue'
import GraphView from '../views/process/GraphView.vue'
import TreeView from '../views/process/TreeView.vue'
import TerminalView from '../views/terminal/TerminalView.vue'
import type { TaskView } from './taskKeys'

/**
 * The panel of the selected task view. Tree and Graph steps open in the Terminal (`reveal`); edited files open
 * in Git (`openGit`, a repo-relative path).
 */
defineProps<{
  view: TaskView
  task: TaskSummary
  detail: TaskDetail | null
  /** Timeline key the Terminal scrolls to. */
  reveal?: string | null
  /** Repo-relative path Git selects. */
  gitPath?: string | null
}>()
defineEmits<{ reveal: [key: string]; openGit: [path: string] }>()
</script>

<template>
  <TerminalView v-if="view === 'terminal'" :task="task" :reveal="reveal" />
  <TreeView
    v-else-if="view === 'tree'"
    :task="task"
    :detail="detail"
    @reveal="$emit('reveal', $event)"
  />
  <GraphView
    v-else-if="view === 'graph'"
    :task="task"
    :detail="detail"
    @reveal="$emit('reveal', $event)"
  />
  <FilesView
    v-else-if="view === 'files'"
    :task="task"
    :detail="detail"
    @open-git="$emit('openGit', $event)"
  />
  <GitView v-else :task="task" :path="gitPath" />
</template>
