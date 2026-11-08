<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { STATUS_LETTER } from './git'
import type { OtherFile, RepoState } from './useRepoStatus'

/**
 * "Elsewhere in the repository": the uncommitted files of the whole repository that are not this task's work,
 * each marked as changed before the task or as not from this task. They are never offered for commit here. When
 * the repository cannot be read, the files the task found already changed when it started are listed instead.
 */
const props = defineProps<{
  state: RepoState
  others: readonly OtherFile[]
  preexisting: readonly string[]
}>()

const { t } = useI18n()

const count = computed(() =>
  props.state.status === 'ready' ? props.others.length : props.preexisting.length,
)
</script>

<template>
  <details v-if="count || state.status === 'error'" class="repo">
    <summary class="repo__summary">
      <span class="repo__title">{{ t('git.repo.title') }}</span>
      <span class="repo__count">{{ t('git.repo.summary', { n: count }, count) }}</span>
    </summary>
    <p class="repo__desc">{{ t('git.repo.desc') }}</p>
    <p v-if="state.status === 'error'" class="repo__error" role="alert">
      {{ t('git.repo.error', { error: state.error }) }}
    </p>
    <ul v-if="state.status === 'ready'" class="repo__list" :aria-label="t('git.repo.listLabel')">
      <li v-for="other in others" :key="other.file.path" class="repo__row">
        <span class="repo__status" :title="t(`git.status.${other.file.status}`)">
          <span aria-hidden="true">{{ STATUS_LETTER[other.file.status] }}</span>
          <span class="sr-only">{{ t(`git.status.${other.file.status}`) }}</span>
        </span>
        <code class="repo__path">{{ other.file.path }}</code>
        <span class="repo__mark" :class="`repo__mark--${other.mark}`">
          {{ t(`git.repo.${other.mark}`) }}
        </span>
      </li>
    </ul>
    <template v-else-if="preexisting.length">
      <p class="repo__desc">
        {{ t('git.preexisting', { n: preexisting.length }, preexisting.length) }}
      </p>
      <p class="repo__desc">{{ t('git.preexistingDesc') }}</p>
      <ul class="repo__list">
        <li v-for="pre in preexisting" :key="pre" class="repo__row">
          <code class="repo__path">{{ pre }}</code>
        </li>
      </ul>
    </template>
  </details>
</template>

<style scoped>
.repo {
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
  font-size: var(--fs-sm);
}

.repo__summary {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-0) var(--space-3);
  min-height: max(2.75rem, var(--target-min));
  padding: var(--space-1) var(--space-3);
  border-radius: var(--radius-md);
  cursor: pointer;
}

.repo__summary:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}

.repo__title {
  font-weight: var(--fw-semibold);
}

.repo__count,
.repo__desc {
  color: var(--text-secondary);
}

.repo__desc,
.repo__error {
  max-width: var(--measure-prose);
  padding: 0 var(--space-3) var(--space-2);
}

.repo__error {
  color: var(--status-danger-fg);
}

.repo__list {
  display: flex;
  flex-direction: column;
  padding: 0 var(--space-2) var(--space-2);
  list-style: none;
}

.repo__row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-0) var(--space-2);
  min-height: var(--control-sm);
  padding: var(--space-0) var(--space-1);
}

.repo__status {
  flex: none;
  width: 2ch;
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  font-weight: var(--fw-semibold);
  text-align: center;
}

.repo__path {
  flex: 1 1 12rem;
  min-width: 0;
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  overflow-wrap: anywhere;
}

.repo__mark {
  padding: 0 var(--space-2);
  border: var(--border-width) solid var(--neutral-border);
  border-radius: var(--radius-pill);
  background: var(--neutral-bg);
  color: var(--neutral-fg);
  font-size: var(--fs-2xs);
  white-space: nowrap;
}

.repo__mark--preexisting {
  border-color: var(--attention-border);
  background: var(--attention-bg);
  color: var(--attention-fg);
}
</style>
