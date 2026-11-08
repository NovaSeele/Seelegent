<script setup lang="ts">
import { nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { GitCommit } from '@/api/types'
import { useFormat } from '@/composables/useFormat'

/**
 * The commits made since the task started, newest first, as a listbox: subject, short id, the turn that made it,
 * author and time. Up/Down, Home/End move the selection; older commits past the cap are counted at the end.
 */
const props = defineProps<{ commits: readonly GitCommit[]; more: number; label: string }>()
const selected = defineModel<string | null>('selected', { required: true })

const { t } = useI18n()
const format = useFormat()
const list = ref<HTMLElement | null>(null)

function move(index: number) {
  const next = props.commits[Math.min(props.commits.length - 1, Math.max(0, index))]
  if (!next) return
  selected.value = next.sha
  void nextTick(() => list.value?.querySelector<HTMLElement>('[aria-selected="true"]')?.focus())
}

function onKeydown(event: KeyboardEvent) {
  const at = props.commits.findIndex((c) => c.sha === selected.value)
  const target =
    event.key === 'ArrowDown'
      ? at + 1
      : event.key === 'ArrowUp'
        ? at - 1
        : event.key === 'Home'
          ? 0
          : event.key === 'End'
            ? props.commits.length - 1
            : null
  if (target === null) return
  event.preventDefault()
  move(target)
}
</script>

<template>
  <div class="gcl">
    <ul ref="list" class="gcl__list" role="listbox" :aria-label="label" @keydown="onKeydown">
      <li
        v-for="(commit, index) in commits"
        :key="commit.sha"
        class="gcl__row"
        :class="{ 'gcl__row--selected': commit.sha === selected }"
        role="option"
        :aria-selected="commit.sha === selected"
        :tabindex="commit.sha === selected || (!selected && index === 0) ? 0 : -1"
        :data-sha="commit.sha"
        @click="selected = commit.sha"
      >
        <span class="gcl__subject">{{ commit.subject || commit.sha.slice(0, 10) }}</span>
        <span class="gcl__meta">
          <code class="gcl__sha">{{ commit.sha.slice(0, 7) }}</code>
          <span v-if="commit.turn" class="gcl__turn">{{
            t('git.history.turn', { n: commit.turn })
          }}</span>
          <span>{{ t('git.history.fileCount', { n: commit.fileCount }, commit.fileCount) }}</span>
          <span class="gcl__by">
            {{ commit.author }} ·
            <time :datetime="commit.authorTime" :title="format.dateTime(commit.authorTime)">
              {{ format.relative(commit.authorTime) }}
            </time>
          </span>
        </span>
      </li>
    </ul>
    <p v-if="more > 0" class="gcl__more">{{ t('git.history.more', { n: more }, more) }}</p>
  </div>
</template>

<style scoped>
.gcl__list {
  display: flex;
  flex-direction: column;
  padding: var(--space-1);
  list-style: none;
}

.gcl__row {
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: var(--space-0);
  min-height: max(2.75rem, var(--target-min));
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm);
  cursor: pointer;
  font-size: var(--fs-sm);
}

.gcl__row:hover {
  background: var(--bg-hover);
}

.gcl__row--selected {
  background: var(--accent-soft);
}

.gcl__row:focus-visible {
  outline: none;
  box-shadow: inset 0 0 0 var(--focus-ring-width) var(--border-focus);
}

.gcl__subject {
  overflow: hidden;
  color: var(--text-primary);
  font-weight: var(--fw-medium);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.gcl__meta {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-0) var(--space-2);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.gcl__sha {
  font-family: var(--font-mono);
}

.gcl__turn {
  font-weight: var(--fw-medium);
}

.gcl__by {
  min-width: 0;
  overflow-wrap: anywhere;
}

.gcl__more {
  padding: var(--space-1) var(--space-3) var(--space-2);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}
</style>
