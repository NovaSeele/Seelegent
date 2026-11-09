<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { TaskDetail, TaskSummary } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Icon from '@/components/ui/Icon.vue'
import IconButton from '@/components/ui/IconButton.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useFormat } from '@/composables/useFormat'
import { isBoolean, readStored, writeStored } from '@/lib/storage'
import { isActive } from '@/lib/taskStatus'

import CommitDialog from './CommitDialog.vue'
import { isDrafts, putDraft, type Drafts } from './git'
import GitDiffPane from './GitDiffPane.vue'
import GitDirList from './GitDirList.vue'
import GitFileList from './GitFileList.vue'
import GitHistory from './GitHistory.vue'
import GitRepoPanel from './GitRepoPanel.vue'
import GitStartNotice from './GitStartNotice.vue'
import GitUpstream from './GitUpstream.vue'
import { useGit } from './useGit'
import { useGitCommands } from './useGitCommands'
import { useGitHistory } from './useGitHistory'
import { useRepoStatus } from './useRepoStatus'
import NotesPanel from './NotesPanel.vue'
import OutsideDiffButton from './OutsideDiffButton.vue'
import { useNotes } from './useNotes'

/**
 * The Git view: what this task committed since it started (GitHistory), the files it changed and has not
 * committed (paths from the repository root) with the selected file's diff and a commit of the ticked ones, and
 * the rest of the repository (GitRepoPanel), never offered. `path` selects a file (opened from the Files view).
 * j/k step through the uncommitted files, an open folder's included (the list holding the file takes the focus).
 * `detail` says which turns have changes outside the edit tools to show.
 */
const props = defineProps<{
  task: TaskSummary
  detail?: TaskDetail | null
  path?: string | null
}>()

const { t } = useI18n()
const format = useFormat()
const openPath = () => props.path
const model = useGit(() => props.task, undefined, openPath)
const { git, files, dirs, expanded, shown, count, sums, selected, selectedFile, diff } = model
const history = useGitHistory(
  () => props.task.id,
  () => git.value,
)
const repo = useRepoStatus(() => git.value)

const list = ref<InstanceType<typeof GitFileList> | null>(null)
const dirList = ref<InstanceType<typeof GitDirList> | null>(null)
const active = computed(() => isActive(props.task))

const WRAP = { name: 'git.wrap', fallback: false, validate: isBoolean }
const wrap = ref(readStored(WRAP))
watch(wrap, (value) => writeStored(WRAP, value))

const DRAFTS = { name: 'git.drafts', fallback: {} as Drafts, validate: isDrafts }
const message = ref(readStored(DRAFTS)[props.task.id]?.message ?? '')
watch(message, (value) =>
  writeStored(DRAFTS, putDraft(readStored(DRAFTS), props.task.id, value, Date.now())),
)

const committing = ref(false)
const notes = useNotes(
  () => props.task.id,
  () => active.value,
)

function step(delta: number) {
  if (!model.step(delta)) return
  list.value?.focusSelected()
  dirList.value?.focusSelected()
}

useGitCommands(
  () => shown.value.length > 0,
  step,
  () => (committing.value = true),
)
</script>

<template>
  <div class="git">
    <p v-if="model.state.value.status === 'loading'" class="git__state">
      <Spinner size="sm" decorative />
      {{ t('git.loading') }}
    </p>
    <div v-else-if="model.state.value.status === 'error'" class="git__state" role="alert">
      <span>{{ t('git.loadError', { error: model.state.value.error }) }}</span>
      <Button size="sm" @click="model.reload">{{ t('common.action.retry') }}</Button>
    </div>
    <EmptyState
      v-else-if="git && !git.repo"
      icon="git"
      :level="3"
      :title="t('git.notRepo')"
      :description="t('git.notRepoDesc', { cwd: task.cwd })"
    />
    <template v-else-if="git">
      <header class="git__bar">
        <span class="git__branch" :title="git.root">
          <Icon name="git" />
          <span>{{ git.branch ?? t('git.noBranch') }}</span>
        </span>
        <GitUpstream :upstream="repo.upstream.value" />
        <span class="git__sum">
          {{ t('git.uncommittedCount', { n: count }, count) }}
          <span class="git__add">+{{ format.number(sums.additions) }}</span>
          <span class="git__del">−{{ format.number(sums.deletions) }}</span>
          <span v-if="model.hidden.value">{{ t('git.sumShown') }}</span>
        </span>
        <span
          v-if="model.state.value.status === 'ready' && model.state.value.refreshError"
          class="git__stale"
          role="alert"
        >
          {{ t('git.loadError', { error: model.state.value.refreshError }) }}
        </span>
        <span class="git__actions">
          <IconButton icon="refresh" size="sm" :label="t('git.refresh')" @click="model.reload" />
          <OutsideDiffButton :task-id="task.id" :detail="detail" />
          <Button size="sm" variant="primary" :disabled="!count" @click="committing = true">
            {{ t('git.commit.open') }}
          </Button>
        </span>
      </header>

      <GitStartNotice :notices="history.notices.value" />
      <GitHistory v-if="history.hasHistory.value" :history="history" :wrap="wrap" />
      <h3 v-if="history.hasHistory.value" class="git__heading">
        {{ t('git.history.uncommitted') }}
      </h3>
      <EmptyState
        v-if="!count"
        icon="git"
        :level="3"
        :title="history.hasHistory.value ? t('git.history.noUncommitted') : t('git.empty')"
        :description="
          history.hasHistory.value ? t('git.history.noUncommittedDesc') : t('git.emptyDesc')
        "
      />
      <div v-else class="git__body">
        <nav class="git__list">
          <GitFileList
            v-if="files.length"
            ref="list"
            v-model:selected="selected"
            :files="files"
            :label="t('git.listLabel')"
          />
          <GitDirList
            ref="dirList"
            v-model:selected="selected"
            v-model:expanded="expanded"
            :dirs="dirs"
          />
        </nav>
        <GitDiffPane
          v-model:wrap="wrap"
          :task-id="task.id"
          :file="selectedFile"
          :diff="diff"
          :notes="notes"
          @retry="model.reloadDiff"
        />
      </div>
      <NotesPanel v-if="count" :notes="notes" :busy="active" @open="selected = $event" />

      <GitRepoPanel
        :state="repo.state.value"
        :others="repo.others.value"
        :preexisting="git.preexisting"
      />

      <CommitDialog
        v-model:open="committing"
        v-model:message="message"
        :files="shown"
        :hidden="model.hidden.value"
        :suggestion="git.suggestion"
        :busy="model.busy.value"
        :active="active"
        :commit="model.commit"
        @suggest="model.suggest"
        @dismiss="model.dismiss"
      />
    </template>
  </div>
</template>

<style scoped>
.git {
  position: relative;
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: var(--space-3);
  min-height: 0;
  padding: var(--space-3) var(--space-4) var(--space-4);
  overflow: auto;
}

.git__state {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-6) var(--space-4);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.git__bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-4);
  font-size: var(--fs-sm);
}

.git__branch {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  font-family: var(--font-mono);
  font-weight: var(--fw-medium);
}

.git__sum {
  display: inline-flex;
  gap: var(--space-2);
  color: var(--text-secondary);
}

.git__add {
  color: var(--diff-add);
}

.git__del {
  color: var(--diff-del);
}

.git__stale {
  color: var(--status-danger-fg);
  font-size: var(--fs-xs);
}

.git__actions {
  display: flex;
  gap: var(--space-2);
  margin-left: auto;
}

.git__body {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.git__list {
  position: relative;
  max-height: 40vh;
  overflow: auto;
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
}

.git__heading {
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
}

@media (width >= 900px) {
  .git__body {
    display: grid;
    grid-template-columns: minmax(16rem, 30%) minmax(0, 1fr);
    align-items: start;
  }

  .git__list {
    position: sticky;
    top: 0;
    max-height: calc(100vh - 16rem);
  }
}

@media (width < 600px) {
  .git {
    padding-inline: var(--space-2);
  }
}
</style>
