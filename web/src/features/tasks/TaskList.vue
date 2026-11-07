<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import Button from '@/components/ui/Button.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import IconButton from '@/components/ui/IconButton.vue'
import Kbd from '@/components/ui/Kbd.vue'
import TextField from '@/components/ui/TextField.vue'
import { useNow } from '@/composables/useNow'
import { stableComputed } from '@/lib/stable'
import { readStored, writeStored } from '@/lib/storage'
import { useTasksStore } from '@/stores/tasks'
import { useUiStore } from '@/stores/ui'

import BulkBar from './BulkBar.vue'
import BulkDialog from './BulkDialog.vue'
import GroupRow from './GroupRow.vue'
import ListFilters from './ListFilters.vue'
import {
  apiQuery,
  buildRows,
  COLLAPSED_GROUPS,
  LIST_GROUPING,
  NEEDS_GROUP,
  rowKey,
  sameRows,
  STATUS_FILTER,
  type ListFilters as Filters,
} from './listModel'
import TaskRow from './TaskRow.vue'
import { useListKeyboard } from './useListKeyboard'
import { useTaskSelection } from './useTaskSelection'

/**
 * The Tasks sidebar: search, filters, grouping by project (a pinned "needs you" group on top), keyboard walk
 * (useListKeyboard), selection with bulk cancel / accept (useTaskSelection), and paging of older tasks from
 * GET /tasks. Rows are keyed by id and read their own task, so a live update touches one row; the row list itself
 * changes only when ids or groups do.
 */
defineProps<{ currentId?: string }>()

const { t } = useI18n()
const tasks = useTasksStore()
const minute = useNow(30_000)

const q = ref('')
const debouncedQ = ref('')
let qTimer: ReturnType<typeof setTimeout> | undefined
watch(q, (value) => {
  clearTimeout(qTimer)
  qTimer = setTimeout(() => (debouncedQ.value = value), 200)
})
onBeforeUnmount(() => clearTimeout(qTimer))

// "/" from anywhere focuses the search (the shortcut lives in useTasksCommands).
const ui = useUiStore()
const searchField = ref<InstanceType<typeof TextField> | null>(null)
const searchFocused = ref(false)
watch(
  () => ui.taskSearchRequested,
  (requested) => {
    if (requested && ui.takeTaskSearchRequest()) {
      void nextTick(() => {
        searchField.value?.focus()
        searchField.value?.select()
      })
    }
  },
  { immediate: true, flush: 'post' },
)

const statuses = ref(readStored(STATUS_FILTER))
const project = ref('')
const grouping = ref(readStored(LIST_GROUPING))
const collapsed = ref(new Set(readStored(COLLAPSED_GROUPS)))
const showFilters = ref(statuses.value.length > 0)
watch(statuses, (value) => writeStored(STATUS_FILTER, value))
watch(grouping, (value) => writeStored(LIST_GROUPING, value))

const filters = computed<Filters>(() => ({
  q: debouncedQ.value,
  statuses: statuses.value,
  project: project.value,
}))
const filtering = computed(() => !!(q.value.trim() || statuses.value.length || project.value))

const rows = stableComputed(
  () =>
    buildRows(
      tasks.summaries.values(),
      filters.value,
      grouping.value,
      collapsed.value,
      minute.value,
    ),
  sameRows,
)
const order = computed(() => rows.value.flatMap((row) => (row.kind === 'task' ? [row.id] : [])))

// Paging: each filter set reads its own first page, then "Load older" continues from its cursor.
const query = computed(() => apiQuery(filters.value))
const more = computed(() => tasks.loaded && tasks.hasMore(query.value))
watch(
  [query, () => tasks.loaded],
  ([value, loaded]) => {
    if (loaded && tasks.hasMore(value)) void tasks.loadPage(value)
  },
  { immediate: true },
)

function toggleGroup(key: string, force?: boolean) {
  const next = new Set(collapsed.value)
  if (force ?? !next.has(key)) next.add(key)
  else next.delete(key)
  collapsed.value = next
  writeStored(COLLAPSED_GROUPS, [...next])
}

const select = useTaskSelection(order)
const { selectMode, selection, selectedIds, eligible, bulk, onToggle, onBulkDone } = select

const list = ref<HTMLElement | null>(null)
const { focusKey, tabStop, onKeydown } = useListKeyboard(rows, list, select, toggleGroup)

const groupLabel = (key: string, label: string) =>
  key === NEEDS_GROUP ? t('tasks.list.needsGroup') : label
</script>

<template>
  <div class="tl">
    <header class="tl__head">
      <h1 class="tl__title">{{ t('nav.tasks.label') }}</h1>
      <IconButton
        icon="filter"
        size="sm"
        :label="t('tasks.list.filters')"
        :pressed="showFilters"
        @click="showFilters = !showFilters"
      />
      <IconButton
        icon="select"
        size="sm"
        :label="selectMode ? t('tasks.select.stop') : t('tasks.select.start')"
        :pressed="selectMode"
        @click="selectMode = !selectMode"
      />
    </header>
    <div class="tl__tools">
      <TextField
        ref="searchField"
        v-model="q"
        type="search"
        icon="search"
        size="sm"
        hide-label
        keyshortcuts="/"
        :label="t('tasks.list.search')"
        :placeholder="t('tasks.list.searchPlaceholder')"
        @focusin="searchFocused = true"
        @focusout="searchFocused = false"
      >
        <template #end>
          <Kbd v-if="!q && !searchFocused" keys="/" size="sm" aria-hidden="true" />
        </template>
      </TextField>
      <ListFilters
        v-if="showFilters"
        v-model:statuses="statuses"
        v-model:project="project"
        v-model:grouping="grouping"
      />
    </div>

    <BulkBar
      v-if="selectMode"
      :selected="selectedIds.length"
      :cancel="eligible.cancel.length"
      :accept="eligible.accept.length"
      @run="bulk = $event"
    />

    <ul
      v-if="rows.length"
      ref="list"
      class="tl__rows"
      :aria-label="t('tasks.list.label')"
      @keydown="onKeydown"
    >
      <template v-for="row in rows" :key="rowKey(row)">
        <GroupRow
          v-if="row.kind === 'group'"
          :row-key="rowKey(row)"
          :label="groupLabel(row.key, row.label)"
          :count="row.count"
          :collapsed="row.collapsed"
          :focusable="tabStop === rowKey(row)"
          @toggle="toggleGroup(row.key)"
          @focus="focusKey = rowKey(row)"
        />
        <TaskRow
          v-else
          :id="row.id"
          :now="minute"
          :current="row.id === currentId"
          :focusable="tabStop === rowKey(row)"
          :select-mode="selectMode"
          :selected="selection.ids.has(row.id)"
          :show-project="grouping === 'flat' || row.group === NEEDS_GROUP"
          @toggle="onToggle"
          @focus="focusKey = `t:${row.id}`"
        />
      </template>
    </ul>
    <EmptyState
      v-else-if="tasks.loaded && !tasks.paging.loading"
      :level="2"
      icon="tasks"
      :title="filtering ? t('tasks.list.noMatch') : t('tasks.list.empty')"
      :description="filtering ? t('tasks.list.noMatchDesc') : t('tasks.list.emptyDesc')"
    />

    <div class="tl__foot">
      <p v-if="tasks.paging.error" class="tl__error" role="alert">
        {{ t('tasks.list.loadError', { error: tasks.paging.error }) }}
      </p>
      <Button
        v-if="more || tasks.paging.error"
        size="sm"
        variant="ghost"
        block
        :loading="tasks.paging.loading"
        @click="tasks.loadPage(query)"
      >
        {{ tasks.paging.error ? t('common.action.retry') : t('tasks.list.loadMore') }}
      </Button>
    </div>

    <BulkDialog
      :action="bulk"
      :ids="bulk ? eligible[bulk] : []"
      @close="bulk = null"
      @done="onBulkDone"
    />
  </div>
</template>

<style scoped>
.tl {
  display: flex;
  flex-direction: column;
  min-height: 100%;
}

.tl__head {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-3) var(--space-3) var(--space-1) var(--space-4);
}

.tl__title {
  flex: 1;
  font-size: var(--fs-lg);
  font-weight: var(--fw-semibold);
}

.tl__tools {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-1) var(--space-3) var(--space-2);
}

.tl__rows {
  display: flex;
  flex-direction: column;
  padding: 0 var(--space-2);
  list-style: none;
}

.tl__foot {
  padding: var(--space-2) var(--space-3) var(--space-4);
}

.tl__error {
  margin-bottom: var(--space-2);
  color: var(--status-danger-fg);
  font-size: var(--fs-xs);
}
</style>
