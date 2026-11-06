<script setup lang="ts">
import { nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { GitFile } from '@/api/types'
import FileIcon from '@/components/ui/FileIcon.vue'
import { useFormat } from '@/composables/useFormat'

import { splitPath, STATUS_LETTER, stepFile } from './git'

/**
 * The task's changed files as a listbox: status letter, icon, name and folder, lines added and removed. A
 * rename is one row naming where it came from. Up/Down, Home/End move the selection (j/k work view-wide).
 */
const props = defineProps<{ files: readonly GitFile[]; label: string }>()
const selected = defineModel<string | null>('selected', { required: true })

const { t } = useI18n()
const format = useFormat()
const list = ref<HTMLElement | null>(null)

function focusSelected() {
  void nextTick(() =>
    list.value
      ?.querySelector<HTMLElement>('[aria-selected="true"]')
      ?.focus({ preventScroll: false }),
  )
}

function onKeydown(event: KeyboardEvent) {
  const delta =
    event.key === 'ArrowDown'
      ? 1
      : event.key === 'ArrowUp'
        ? -1
        : event.key === 'Home'
          ? -Infinity
          : event.key === 'End'
            ? Infinity
            : 0
  if (!delta) return
  event.preventDefault()
  const next =
    Math.abs(delta) === Infinity
      ? (props.files[delta > 0 ? props.files.length - 1 : 0]?.path ?? null)
      : stepFile(props.files, selected.value, delta)
  if (next) {
    selected.value = next
    focusSelected()
  }
}

defineExpose({ focusSelected })
</script>

<template>
  <ul ref="list" class="gfl" role="listbox" :aria-label="label" @keydown="onKeydown">
    <li
      v-for="file in files"
      :key="file.path"
      class="gfl__row"
      :class="{ 'gfl__row--selected': file.path === selected }"
      role="option"
      :aria-selected="file.path === selected"
      :tabindex="file.path === selected || (!selected && file === files[0]) ? 0 : -1"
      @click="selected = file.path"
    >
      <span
        class="gfl__status"
        :class="`gfl__status--${file.status}`"
        :title="t(`git.status.${file.status}`)"
      >
        <span aria-hidden="true">{{ STATUS_LETTER[file.status] }}</span>
        <span class="sr-only">{{ t(`git.status.${file.status}`) }}</span>
      </span>
      <FileIcon :path="file.path" :dim="file.status === 'deleted'" />
      <span class="gfl__path" :title="file.oldPath ? `${file.oldPath} → ${file.path}` : file.path">
        <span class="gfl__name" :class="{ 'gfl__name--deleted': file.status === 'deleted' }">
          {{ splitPath(file.path).name }}
        </span>
        <span class="gfl__dir">{{ splitPath(file.path).dir }}</span>
        <span v-if="file.oldPath" class="gfl__from">
          {{ t('git.renamedFrom', { path: file.oldPath }) }}
        </span>
      </span>
      <span v-if="file.binary" class="gfl__count">{{ t('git.binary') }}</span>
      <span v-else class="gfl__count">
        <span class="gfl__add">+{{ format.number(file.additions) }}</span>
        <span class="gfl__del">−{{ format.number(file.deletions) }}</span>
      </span>
    </li>
  </ul>
</template>

<style scoped>
.gfl {
  display: flex;
  flex-direction: column;
  padding: var(--space-1);
  list-style: none;
}

.gfl__row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: var(--control-md);
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm);
  cursor: pointer;
  font-size: var(--fs-sm);
}

.gfl__row:hover {
  background: var(--bg-hover);
}

.gfl__row--selected {
  background: var(--accent-soft);
}

.gfl__row:focus-visible {
  outline: none;
  box-shadow: inset 0 0 0 var(--focus-ring-width) var(--border-focus);
}

.gfl__status {
  flex: none;
  width: 2ch;
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  font-weight: var(--fw-semibold);
  text-align: center;
}

.gfl__status--added,
.gfl__status--untracked {
  color: var(--diff-add);
}

.gfl__status--modified,
.gfl__status--renamed {
  color: var(--attention-fg);
}

.gfl__status--deleted {
  color: var(--diff-del);
}

.gfl__path {
  display: flex;
  flex: 1;
  flex-wrap: wrap;
  align-items: baseline;
  column-gap: var(--space-2);
  min-width: 0;
}

.gfl__name {
  overflow: hidden;
  color: var(--text-primary);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.gfl__name--deleted {
  color: var(--text-muted);
  text-decoration: line-through;
}

.gfl__dir,
.gfl__from {
  min-width: 0;
  overflow: hidden;
  color: var(--text-muted);
  font-size: var(--fs-xs);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.gfl__from {
  flex-basis: 100%;
}

.gfl__count {
  display: flex;
  flex: none;
  gap: var(--space-1);
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
}

.gfl__add {
  color: var(--diff-add);
}

.gfl__del {
  color: var(--diff-del);
}
</style>
