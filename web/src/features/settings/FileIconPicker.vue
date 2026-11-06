<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import FileIcon from '@/components/ui/FileIcon.vue'
import { FILE_ICON_SET_IDS, FILE_ICON_SETS, type FileIconSetId } from '@/lib/fileicons'

/**
 * File icon set picker: one card per set with its name, where it comes from and a preview of a few entries
 * drawn in that set (always that set, whatever is chosen). Native radios give arrow keys and semantics.
 */
const model = defineModel<FileIconSetId>({ required: true })

const { t } = useI18n()

/** What the preview lists: a folder and five common file types of this project. */
const SAMPLES = ['src/', 'main.ts', 'App.vue', 'server.go', 'api.json', 'README.md'] as const

function name(id: FileIconSetId): string {
  if (id === 'seelegent-mono') return t('settings.appearance.fileIcons.seelegentMono')
  if (id === 'off') return t('settings.appearance.fileIcons.off')
  return FILE_ICON_SETS[id].name
}

function origin(id: FileIconSetId): string {
  const set = FILE_ICON_SETS[id]
  if (set.kind === 'off') return t('settings.appearance.fileIcons.namesOnly')
  if (set.kind === 'builtin') return t('settings.appearance.fileIcons.builtin')
  return `${set.license} · ${set.source}`
}
</script>

<template>
  <div class="picker" role="radiogroup" aria-labelledby="setting-fileIcons">
    <label
      v-for="id in FILE_ICON_SET_IDS"
      :key="id"
      class="card"
      :class="{ 'card--checked': model === id }"
      :data-icon-set="id"
    >
      <input v-model="model" class="sr-only" type="radio" name="fileIcons" :value="id" />
      <span class="card__head">
        <span class="card__name">{{ name(id) }}</span>
        <span class="card__origin" :title="origin(id)">{{ origin(id) }}</span>
      </span>
      <ul class="card__preview" aria-hidden="true">
        <li v-for="sample in SAMPLES" :key="sample" class="card__file">
          <FileIcon :path="sample" :set="id" lead />{{ sample.replace(/\/$/, '') }}
        </li>
      </ul>
    </label>
  </div>
</template>

<style scoped>
.picker {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(188px, 1fr));
  gap: var(--space-3);
}

.card {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
  padding: var(--space-2) var(--space-3) var(--space-3);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
  cursor: pointer;
  transition: border-color var(--duration-fast) var(--ease-standard);
}

.card:hover {
  border-color: var(--border-strong);
}

.card--checked,
.card--checked:hover {
  border-color: var(--accent);
  box-shadow: 0 0 0 1px var(--accent);
}

.card:has(:focus-visible) {
  box-shadow: var(--focus-ring);
}

.card__head {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.card__name {
  color: var(--text-primary);
  font-size: var(--fs-sm);
  font-weight: var(--fw-medium);
}

.card__origin {
  overflow: hidden;
  color: var(--text-muted);
  font-size: var(--fs-xs);
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* A miniature file list on the code background, two columns so six entries stay short. */
.card__preview {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-1) var(--space-2);
  margin: 0;
  padding: var(--space-2);
  border-radius: var(--radius-md);
  background: var(--bg-code);
  list-style: none;
}

.card__file {
  overflow: hidden;
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  line-height: var(--lh-tight);
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (width < 600px) {
  .picker {
    grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  }
}
</style>
