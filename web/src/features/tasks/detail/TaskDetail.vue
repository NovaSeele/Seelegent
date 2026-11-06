<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import Button from '@/components/ui/Button.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Spinner from '@/components/ui/Spinner.vue'
import Tabs from '@/components/ui/Tabs.vue'
import { readStored, writeStored } from '@/lib/storage'

import AttentionBanner from './AttentionBanner.vue'
import FollowupComposer from './FollowupComposer.vue'
import TaskDialogs, { type TaskDialog } from './TaskDialogs.vue'
import TaskHeader from './TaskHeader.vue'
import { TASK_VIEW, TASK_VIEWS, VIEW_ICONS, type TaskView } from './taskKeys'
import TaskViews from './TaskViews.vue'
import { useTaskActions } from './useTaskActions'
import { useTaskCommands } from './useTaskCommands'
import { useTaskDetail } from './useTaskDetail'

/**
 * The open task: header and actions, why it waits (banner, question), the five views (keys 1-5: Terminal,
 * Tree, Graph, Files, Git) and the follow-up composer. Mounted per task (keyed by id), so nothing of one task
 * can leak into another's view.
 */
const props = defineProps<{ taskId: string; titleLevel: 1 | 2; showBack: boolean }>()

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

const { summary, detail, error, reload } = useTaskDetail(() => props.taskId)
const actions = useTaskActions(() => props.taskId)

// The view comes from ?view= (links such as "Review changes" open Git) or the last one used.
const isView = (value: unknown): value is TaskView =>
  typeof value === 'string' && (TASK_VIEWS as readonly string[]).includes(value)
const view = ref<TaskView>(isView(route.query.view) ? route.query.view : readStored(TASK_VIEW))
watch(view, (value) => {
  writeStored(TASK_VIEW, value)
  // A reveal or file pick is for the next time that view opens from a link, not for later tab switches.
  if (value !== 'terminal') revealKey.value = null
  if (value !== 'git') gitPath.value = null
  if (route.name === 'tasks' && route.query.view && route.query.view !== value) {
    void router.replace({ query: { ...route.query, view: undefined } })
  }
})
watch(
  () => route.query.view,
  (value) => {
    if (isView(value)) view.value = value
  },
)

const tabs = computed(() =>
  TASK_VIEWS.map((value) => ({ value, label: t(`task.views.${value}`), icon: VIEW_ICONS[value] })),
)

// A step opened from Tree or Graph scrolls the Terminal to it; a file from Files selects it in Git.
const revealKey = ref<string | null>(null)
const gitPath = ref<string | null>(null)
function openIn(target: 'terminal' | 'git', value: string) {
  if (target === 'terminal') revealKey.value = value
  else gitPath.value = value
  view.value = target
}

const dialog = ref<TaskDialog>(null)
const composer = ref<InstanceType<typeof FollowupComposer> | null>(null)

useTaskCommands({
  task: () => summary.value,
  actions,
  focusFollowup: () => composer.value?.focus(),
  openDialog: (value) => (dialog.value = value),
  setView: (value) => (view.value = value),
})

const missing = computed(() => !summary.value && !!error.value)
const loadingFirst = computed(() => !summary.value && !error.value)
</script>

<template>
  <div v-if="missing" class="td-state">
    <EmptyState
      icon="warning"
      :title="t('task.detail.notFound')"
      :description="error ?? undefined"
    />
    <div class="td-state__actions">
      <Button size="sm" @click="reload">{{ t('common.action.retry') }}</Button>
      <Button size="sm" variant="ghost" :to="{ name: 'tasks' }">{{ t('nav.tasks.label') }}</Button>
    </div>
  </div>
  <div v-else-if="loadingFirst" class="td-state">
    <Spinner :label="t('task.detail.loading')" />
  </div>
  <article v-else-if="summary" class="td" :aria-label="summary.title">
    <div class="td__top">
      <TaskHeader
        :task="summary"
        :title-level="titleLevel"
        :show-back="showBack"
        :actions="actions"
        @followup="composer?.focus()"
        @review="(verdict) => (dialog = { kind: 'review', verdict })"
        @confirm="(kind) => (dialog = { kind })"
      />
      <AttentionBanner
        :task="summary"
        :actions="actions"
        @confirm="(kind) => (dialog = { kind })"
      />
      <section v-if="detail?.error" class="td__error" role="alert">
        <strong>{{ t('task.detail.error') }}</strong>
        <pre class="td__error-text">{{ detail.error }}</pre>
      </section>
    </div>
    <Tabs
      v-model="view"
      :items="tabs"
      :label="t('task.views.label')"
      id-prefix="task-view"
      class="td__tabs"
    />
    <div
      :id="`task-view-panel-${view}`"
      class="td__panel"
      role="tabpanel"
      :aria-labelledby="`task-view-tab-${view}`"
    >
      <TaskViews
        :view="view"
        :task="summary"
        :detail="detail"
        :reveal="revealKey"
        :git-path="gitPath"
        @reveal="openIn('terminal', $event)"
        @open-git="openIn('git', $event)"
      />
    </div>
    <FollowupComposer ref="composer" :task="summary" :actions="actions" />
    <TaskDialogs v-model="dialog" :task="summary" :actions="actions" />
  </article>
</template>

<style scoped>
.td {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}

.td__top {
  flex: none;
  max-height: 50%;
  overflow: auto;
}

.td__error {
  margin: 0 var(--space-5) var(--space-3);
  padding: var(--space-3) var(--space-4);
  border: var(--border-width) solid var(--status-danger-border);
  border-radius: var(--radius-lg);
  background: var(--status-danger-bg);
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
}

.td__error-text {
  margin-top: var(--space-1);
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.td__tabs {
  flex: none;
  padding: 0 var(--space-4);
}

.td__panel {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-height: 200px;
}

.td-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  min-height: 100%;
  padding: var(--space-6);
}

.td-state__actions {
  display: flex;
  gap: var(--space-2);
}

@media (width < 600px) {
  .td__error {
    margin: 0 var(--space-4) var(--space-3);
  }

  .td__tabs {
    padding: 0 var(--space-2);
  }
}
</style>
