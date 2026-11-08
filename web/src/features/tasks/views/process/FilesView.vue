<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { TaskDetail, TaskSummary } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import FileIcon from '@/components/ui/FileIcon.vue'
import { baseName } from '@/lib/paths'

import { buildFiles } from './files'
import ProcessFrame from './ProcessFrame.vue'
import { useProcess } from './useProcess'

/**
 * The Files view: every file the worker read or edited, grouped by folder, with reads, edits and the turns that
 * touched it. An edited file inside the repository opens in the Git view (by its repo-relative path).
 */
const props = defineProps<{ task: TaskSummary; detail: TaskDetail | null }>()
defineEmits<{ openGit: [path: string] }>()

const { t } = useI18n()
const model = useProcess(props.task.id, () => props.detail)
const files = computed(() =>
  buildFiles(model.steps.value, props.task.cwd, props.detail?.repoRoot ?? props.task.cwd),
)
const total = computed(() => files.value.folders.reduce((sum, f) => sum + f.files.length, 0))
const tiles = computed(() => [
  { key: 'total', value: total.value },
  { key: 'read', value: files.value.read },
  { key: 'edited', value: files.value.edited },
  { key: 'outside', value: files.value.outside },
])
</script>

<template>
  <ProcessFrame
    :model="model"
    :empty="!total"
    icon="file"
    :empty-title="t('process.files.empty')"
    :empty-desc="t('process.files.emptyDesc')"
  >
    <div class="files">
      <dl class="files__tiles">
        <div v-for="tile in tiles" :key="tile.key" class="files__tile">
          <dt>{{ t(`process.files.${tile.key}`) }}</dt>
          <dd>{{ tile.value }}</dd>
        </div>
      </dl>
      <table class="files__table">
        <caption class="sr-only">
          {{
            t('process.files.label')
          }}
        </caption>
        <thead>
          <tr>
            <th scope="col">{{ t('process.files.file') }}</th>
            <th scope="col" class="files__num">{{ t('process.files.reads') }}</th>
            <th scope="col" class="files__num">{{ t('process.files.edits') }}</th>
            <th scope="col" class="files__turns">{{ t('process.files.turns') }}</th>
            <th scope="col">
              <span class="sr-only">{{ t('process.files.openGit') }}</span>
            </th>
          </tr>
        </thead>
        <tbody v-for="group in files.folders" :key="group.folder">
          <tr class="files__folder">
            <th scope="colgroup" colspan="5">
              <FileIcon :path="`${group.folder || '.'}/`" lead />
              {{ group.folder || t('process.files.root') }}
              <span v-if="group.files[0]?.outside" class="files__tag">
                {{ t('process.files.outsideTag') }}
              </span>
            </th>
          </tr>
          <tr v-for="file in group.files" :key="file.rel" class="files__row">
            <th scope="row" class="files__name" :title="file.rel">
              <FileIcon :path="file.rel" lead />
              {{ baseName(file.rel) }}
            </th>
            <td class="files__num">{{ file.reads || '' }}</td>
            <td class="files__num files__num--edit">{{ file.edits || '' }}</td>
            <td class="files__turns">{{ file.turns.join(', ') }}</td>
            <td class="files__action">
              <Button
                v-if="file.edits && file.gitPath"
                size="sm"
                variant="ghost"
                @click="$emit('openGit', file.gitPath)"
              >
                {{ t('process.files.openGit') }}
              </Button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </ProcessFrame>
</template>

<style scoped>
.files {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  padding: var(--space-3) var(--space-4) var(--space-5);
}

.files__tiles {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--space-2);
}

.files__tile {
  padding: var(--space-2) var(--space-3);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
}

.files__tile dt {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.files__tile dd {
  margin: 0;
  font-size: var(--fs-lg);
  font-weight: var(--fw-semibold);
  font-variant-numeric: tabular-nums;
}

.files__table {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--fs-sm);
}

.files__table thead th {
  padding: var(--space-1) var(--space-2);
  border-bottom: var(--border-width) solid var(--border-subtle);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
  text-align: left;
}

.files__folder th {
  padding: var(--space-3) var(--space-2) var(--space-1);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-semibold);
  text-align: left;
  overflow-wrap: anywhere;
}

.files__tag {
  margin-left: var(--space-1);
  color: var(--status-waiting-fg);
  font-weight: var(--fw-regular);
}

.files__row:hover {
  background: var(--bg-hover);
  --text-muted: var(--text-primary);
  --text-secondary: var(--text-primary);
}

.files__row td,
.files__row th {
  padding: var(--space-1) var(--space-2);
  border-bottom: var(--border-width) solid var(--border-subtle);
}

.files__name {
  max-width: 0;
  overflow: hidden;
  font-weight: var(--fw-regular);
  text-align: left;
  text-overflow: ellipsis;
  white-space: nowrap;
  width: 60%;
}

.files__num {
  width: 4.5em;
  color: var(--text-secondary);
  font-variant-numeric: tabular-nums;
  text-align: right;
}

.files__table thead .files__num {
  text-align: right;
}

.files__num--edit {
  color: var(--stage-edit);
  font-weight: var(--fw-semibold);
}

.files__turns {
  color: var(--text-muted);
  font-size: var(--fs-xs);
}

.files__action {
  text-align: right;
  white-space: nowrap;
}

@media (width < 600px) {
  .files {
    padding-inline: var(--space-2);
  }

  .files__tiles {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .files__turns {
    display: none;
  }
}
</style>
