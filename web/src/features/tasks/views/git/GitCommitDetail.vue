<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type { FileDiff, GitFile } from '@/api/types'
import DiffView from '@/components/diff/DiffView.vue'
import Button from '@/components/ui/Button.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useFormat } from '@/composables/useFormat'

import GitFileList from './GitFileList.vue'
import type { CommitState } from './useGitHistory'

/**
 * One of the task's commits: its message, author, time and turn, the files it changed (a listbox) and the diff of
 * the chosen file against the commit's first parent. View only. A diff left out because the commit's diff was
 * over the size limit says so.
 */
defineProps<{
  state: CommitState
  selection: { file: GitFile; diff: FileDiff | undefined } | null
  wrap: boolean
}>()
const file = defineModel<string | null>('file', { required: true })
const emit = defineEmits<{ retry: [] }>()

const { t } = useI18n()
const format = useFormat()
</script>

<template>
  <section class="gcd" :aria-label="t('git.history.commitLabel')">
    <p v-if="state.status === 'loading'" class="gcd__state">
      <Spinner size="sm" decorative />
      {{ t('git.history.loading') }}
    </p>
    <div v-else-if="state.status === 'error'" class="gcd__state" role="alert">
      <span>{{ t('git.history.error', { error: state.error }) }}</span>
      <Button size="sm" @click="emit('retry')">{{ t('common.action.retry') }}</Button>
    </div>
    <template v-else-if="state.status === 'ready'">
      <header class="gcd__head">
        <h4 class="gcd__subject">{{ state.detail.subject }}</h4>
        <p class="gcd__meta">
          <code>{{ state.detail.sha.slice(0, 12) }}</code>
          <span v-if="state.detail.turn">{{
            t('git.history.turn', { n: state.detail.turn })
          }}</span>
          <span v-else-if="state.detail.outsideTurns" class="gcd__outside">{{
            t('git.history.outside')
          }}</span>
          <span>
            {{ state.detail.author }} ·
            <time :datetime="state.detail.authorTime">
              {{ format.dateTime(state.detail.authorTime) }}
            </time>
          </span>
        </p>
        <p v-if="state.detail.body" class="gcd__body">{{ state.detail.body }}</p>
      </header>
      <p v-if="!state.detail.files.length" class="gcd__state">{{ t('git.history.noFiles') }}</p>
      <template v-else>
        <nav class="gcd__files">
          <GitFileList
            v-model:selected="file"
            :files="state.detail.files"
            :label="t('git.history.commitFiles')"
          />
        </nav>
        <p v-if="state.detail.truncated" class="gcd__note">{{ t('git.diff.truncated') }}</p>
        <template v-if="selection">
          <code class="gcd__path">{{ selection.file.path }}</code>
          <DiffView v-if="selection.diff" :diff="selection.diff" :wrap="wrap" />
          <p v-else class="gcd__note">{{ t('git.history.diffSkipped') }}</p>
        </template>
      </template>
    </template>
  </section>
</template>

<style scoped>
.gcd {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}

.gcd__state {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-6) var(--space-4);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.gcd__head {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.gcd__subject {
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
  overflow-wrap: anywhere;
}

.gcd__meta {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-0) var(--space-3);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.gcd__outside {
  color: var(--text-primary);
  font-weight: var(--fw-medium);
}

.gcd__meta code,
.gcd__path {
  font-family: var(--font-mono);
}

.gcd__body {
  max-width: var(--measure-prose);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.gcd__files {
  max-height: 14rem;
  overflow: auto;
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
}

.gcd__path {
  font-size: var(--fs-sm);
  overflow-wrap: anywhere;
}

.gcd__note {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}
</style>
