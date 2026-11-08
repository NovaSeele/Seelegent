<script setup lang="ts">
import { computed, nextTick, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import type { GitCommit } from '@/api/types'
import { useFormat } from '@/composables/useFormat'

/**
 * The commits made since the task started, newest first, as a listbox: subject, short id, the turn that made it,
 * author and time. Commits made while none of the task's turns ran (someone else's, or between turns) form a
 * second, muted group after the task's own. Up/Down, Home/End move the selection across both groups; older
 * commits past the cap are counted at the end.
 */
const props = defineProps<{ commits: readonly GitCommit[]; more: number; label: string }>()
const selected = defineModel<string | null>('selected', { required: true })

const { t } = useI18n()
const format = useFormat()
const list = ref<HTMLElement | null>(null)
const ownId = useId()
const othersId = useId()

const own = computed(() => props.commits.filter((c) => !c.outsideTurns))
const others = computed(() => props.commits.filter((c) => c.outsideTurns))
// Keyboard order is the order on screen.
const ordered = computed(() => [...own.value, ...others.value])
const groups = computed(() =>
  others.value.length
    ? [
        { id: ownId, title: t('git.history.ownGroup'), hint: '', commits: own.value },
        {
          id: othersId,
          title: t('git.history.othersGroup'),
          hint: t('git.history.othersHint'),
          commits: others.value,
        },
      ].filter((g) => g.commits.length)
    : [{ id: '', title: '', hint: '', commits: own.value }],
)

function move(index: number) {
  const next = ordered.value[Math.min(ordered.value.length - 1, Math.max(0, index))]
  if (!next) return
  selected.value = next.sha
  void nextTick(() => list.value?.querySelector<HTMLElement>('[aria-selected="true"]')?.focus())
}

function onKeydown(event: KeyboardEvent) {
  const at = ordered.value.findIndex((c) => c.sha === selected.value)
  const target =
    event.key === 'ArrowDown'
      ? at + 1
      : event.key === 'ArrowUp'
        ? at - 1
        : event.key === 'Home'
          ? 0
          : event.key === 'End'
            ? ordered.value.length - 1
            : null
  if (target === null) return
  event.preventDefault()
  move(target)
}

const focusable = (commit: GitCommit) =>
  commit.sha === selected.value || (!selected.value && commit.sha === ordered.value[0]?.sha)
</script>

<template>
  <div class="gcl">
    <div ref="list" class="gcl__list" role="listbox" :aria-label="label" @keydown="onKeydown">
      <div
        v-for="group in groups"
        :key="group.id || 'all'"
        class="gcl__group"
        :role="group.id ? 'group' : undefined"
        :aria-labelledby="group.id || undefined"
        :data-group="group.id === othersId ? 'others' : 'own'"
      >
        <div v-if="group.id" role="presentation" class="gcl__heading">
          <span :id="group.id" class="gcl__title">{{ group.title }}</span>
          <span v-if="group.hint" class="gcl__hint">{{ group.hint }}</span>
        </div>
        <div
          v-for="commit in group.commits"
          :key="commit.sha"
          class="gcl__row"
          :class="{
            'gcl__row--selected': commit.sha === selected,
            'gcl__row--other': commit.outsideTurns,
          }"
          role="option"
          :aria-selected="commit.sha === selected"
          :tabindex="focusable(commit) ? 0 : -1"
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
        </div>
      </div>
    </div>
    <p v-if="more > 0" class="gcl__more">{{ t('git.history.more', { n: more }, more) }}</p>
  </div>
</template>

<style scoped>
.gcl__list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-1);
}

.gcl__group {
  display: flex;
  flex-direction: column;
}

.gcl__group + .gcl__group {
  padding-top: var(--space-2);
  border-top: var(--border-width) solid var(--border-subtle);
}

.gcl__heading {
  display: flex;
  flex-direction: column;
  gap: var(--space-0);
  padding: var(--space-1) var(--space-2);
}

.gcl__title {
  color: var(--text-primary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-semibold);
}

.gcl__hint {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
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

.gcl__row--other .gcl__subject {
  color: var(--text-secondary);
  font-weight: var(--fw-regular);
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
