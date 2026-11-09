<script setup lang="ts">
import { computed, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import DiffView from '@/components/diff/DiffView.vue'
import Button from '@/components/ui/Button.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import Spinner from '@/components/ui/Spinner.vue'

import GitCommitDetail from './GitCommitDetail.vue'
import GitCommitList from './GitCommitList.vue'
import GitFileList from './GitFileList.vue'
import type { GitHistoryModel, HistoryMode } from './useGitHistory'

/**
 * "Committed in this task": what the worker (or anyone) committed since the task started. Two ways to look at it:
 * the commits, each with its own diff, or the files they changed with each file's whole change since the start
 * (uncommitted edits made after the commit included, and others' commits to the same file). Commits made outside
 * the task's turns are listed apart and their files are not the task's. View only: the commit dialog takes the
 * uncommitted files.
 */
const props = defineProps<{ history: GitHistoryModel; wrap: boolean }>()

const { t } = useI18n()
const h = props.history
const titleId = useId()

// The task's own commits (older ones the list leaves out included); the others are counted in their group.
const total = computed(() => h.own.value)
const options = computed(() => [
  {
    value: 'commits' as HistoryMode,
    label: t('git.history.commits', { n: total.value }, total.value),
  },
  {
    value: 'files' as HistoryMode,
    label: t('git.history.files', { n: h.files.value.length }, h.files.value.length),
  },
])
</script>

<template>
  <section class="hist" :aria-labelledby="titleId">
    <header class="hist__bar">
      <div class="hist__intro">
        <h3 :id="titleId" class="hist__title">{{ t('git.history.title') }}</h3>
        <p class="hist__desc">{{ t('git.history.desc') }}</p>
      </div>
      <SegmentedControl
        v-model="h.mode.value"
        :options="options"
        :label="t('git.history.mode')"
        size="sm"
      />
    </header>

    <div v-if="h.mode.value === 'commits'" class="hist__body">
      <nav class="hist__list">
        <GitCommitList
          v-model:selected="h.selectedCommit.value"
          :commits="h.commits.value"
          :more="h.more.value"
          :others-total="h.others.value"
          :label="t('git.history.commitsLabel')"
        />
      </nav>
      <GitCommitDetail
        v-model:file="h.commitFile.value"
        :state="h.commit.value"
        :selection="h.commitDiff.value"
        :wrap="wrap"
        @retry="h.reloadCommit"
      />
    </div>

    <div v-else class="hist__body">
      <nav class="hist__list">
        <GitFileList
          v-model:selected="h.selectedFile.value"
          :files="h.files.value"
          :label="t('git.history.filesLabel')"
        />
      </nav>
      <section class="hist__diff" :aria-label="t('git.history.sinceStart')">
        <p v-if="h.file.value" class="hist__path">
          <code>{{ h.file.value.path }}</code>
          <span>{{ t('git.history.sinceStart') }}</span>
        </p>
        <p v-if="h.file.value" class="hist__note">{{ t('git.history.sinceStartNote') }}</p>
        <p v-if="h.fileDiff.value.status === 'loading'" class="hist__state">
          <Spinner size="sm" decorative />
          {{ t('git.diff.loading') }}
        </p>
        <div v-else-if="h.fileDiff.value.status === 'error'" class="hist__state" role="alert">
          <span>{{ t('git.diff.error', { error: h.fileDiff.value.error }) }}</span>
          <Button size="sm" @click="h.reloadFileDiff">{{ t('common.action.retry') }}</Button>
        </div>
        <DiffView
          v-else-if="h.fileDiff.value.status === 'ready'"
          :diff="h.fileDiff.value.diff"
          :wrap="wrap"
        />
      </section>
    </div>
  </section>
</template>

<style scoped>
.hist {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.hist__bar {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  justify-content: space-between;
  gap: var(--space-2) var(--space-4);
}

.hist__intro {
  min-width: 0;
}

.hist__title {
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
}

.hist__desc {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.hist__body {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.hist__list {
  position: relative;
  max-height: 40vh;
  overflow: auto;
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
}

.hist__diff {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}

.hist__path {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-0) var(--space-2);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.hist__path code {
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: var(--fs-sm);
  overflow-wrap: anywhere;
}

.hist__note {
  max-width: var(--measure-prose);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.hist__state {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-6) var(--space-4);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

@media (width >= 900px) {
  .hist__body {
    display: grid;
    grid-template-columns: minmax(16rem, 30%) minmax(0, 1fr);
    align-items: start;
  }
}
</style>
