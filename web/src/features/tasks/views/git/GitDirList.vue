<script setup lang="ts">
import { useId } from 'vue'
import { useI18n } from 'vue-i18n'

import type { GitDir } from '@/api/types'
import FileIcon from '@/components/ui/FileIcon.vue'
import Icon from '@/components/ui/Icon.vue'
import { useFormat } from '@/composables/useFormat'
import { formatBytes } from '@/lib/memory'

import GitFileList from './GitFileList.vue'

/**
 * The task's large untracked folders (TaskGit.untrackedDirs), each one row: "dir/ · N files · size". A row opens
 * (click, Enter, Space) to list the folder's first files, which select like the other changed files; the rest
 * are counted. A folder is never committed as a whole: its listed files are offered one by one once it is open.
 */
const props = defineProps<{ dirs: readonly GitDir[] }>()
const selected = defineModel<string | null>('selected', { required: true })
const expanded = defineModel<ReadonlySet<string>>('expanded', { required: true })

const { t } = useI18n()
const format = useFormat()

function toggle(path: string) {
  const next = new Set(expanded.value)
  if (next.has(path)) next.delete(path)
  else next.add(path)
  expanded.value = next
}

const base = useId()
const listId = (dir: GitDir) => `${base}-${props.dirs.indexOf(dir)}`
</script>

<template>
  <ul class="gdl" :aria-label="t('git.dir.label')">
    <li v-for="dir in dirs" :key="dir.path" class="gdl__item">
      <button
        type="button"
        class="gdl__row"
        :aria-expanded="expanded.has(dir.path)"
        :aria-controls="expanded.has(dir.path) ? listId(dir) : undefined"
        @click="toggle(dir.path)"
      >
        <Icon
          :name="expanded.has(dir.path) ? 'chevronDown' : 'chevronRight'"
          class="gdl__chevron"
        />
        <span class="gdl__status" :title="t('git.status.untracked')">
          <span aria-hidden="true">U</span>
          <span class="sr-only">{{ t('git.status.untracked') }}</span>
        </span>
        <FileIcon :path="dir.path" />
        <span class="gdl__path">{{ dir.path }}</span>
        <span class="gdl__meta">
          {{ t('git.dir.files', { n: format.number(dir.count) }, dir.count) }} ·
          {{ formatBytes(format, dir.bytes) }}
        </span>
      </button>
      <div v-if="expanded.has(dir.path)" :id="listId(dir)" class="gdl__files">
        <GitFileList
          v-model:selected="selected"
          :files="dir.files"
          :label="t('git.dir.listLabel', { path: dir.path })"
        />
        <p v-if="dir.count > dir.files.length" class="gdl__more">
          {{
            t(
              'git.dir.more',
              { n: format.number(dir.count - dir.files.length) },
              dir.count - dir.files.length,
            )
          }}
        </p>
      </div>
    </li>
  </ul>
</template>

<style scoped>
.gdl {
  display: flex;
  flex-direction: column;
  padding: 0 var(--space-1) var(--space-1);
  list-style: none;
}

.gdl__row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  min-height: var(--control-md);
  padding: var(--space-1) var(--space-2);
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-primary);
  cursor: pointer;
  font: inherit;
  font-size: var(--fs-sm);
  text-align: left;
}

.gdl__row:hover {
  background: var(--bg-hover);
}

.gdl__row:focus-visible {
  outline: none;
  box-shadow: inset 0 0 0 var(--focus-ring-width) var(--border-focus);
}

.gdl__chevron {
  flex: none;
  color: var(--text-muted);
}

.gdl__status {
  flex: none;
  width: 2ch;
  color: var(--diff-add);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  font-weight: var(--fw-semibold);
  text-align: center;
}

.gdl__path {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.gdl__meta {
  flex: none;
  color: var(--text-muted);
  font-size: var(--fs-xs);
}

.gdl__row:hover .gdl__meta {
  color: var(--text-primary);
}

.gdl__files {
  padding-left: var(--space-4);
}

.gdl__more {
  padding: var(--space-1) var(--space-3);
  color: var(--text-muted);
  font-size: var(--fs-xs);
}
</style>
