<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import Button from '@/components/ui/Button.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import IconButton from '@/components/ui/IconButton.vue'
import TextField from '@/components/ui/TextField.vue'
import { useNow } from '@/composables/useNow'
import { stableComputed } from '@/lib/stable'
import { readStored, writeStored } from '@/lib/storage'
import { awaitsReview, canCancel } from '@/lib/taskStatus'
import { useCommandsStore } from '@/stores/commands'
import { useTasksStore } from '@/stores/tasks'

import BulkBar from './BulkBar.vue'
import BulkDialog, { type BulkAction } from './BulkDialog.vue'
import GroupRow from './GroupRow.vue'
import ListFilters from './ListFilters.vue'
import {
  apiQuery,
  buildRows,
  COLLAPSED_GROUPS,
  LIST_GROUPING,
  listKey,
  NEEDS_GROUP,
  rowKey,
  sameRows,
  STATUS_FILTER,
  type ListFilters as Filters,
} from './listModel'
import { EMPTY_SELECTION, extendTo, prune, selectAll, toggle } from './selection'
import TaskRow from './TaskRow.vue'

/**
 * The Tasks sidebar: search, filters, grouping by project (a pinned "needs you" group on top), keyboard walk,
 * selection with bulk cancel / accept, and paging of older tasks from GET /tasks. Rows are keyed by id and read
 * their own task, so a live update touches one row; the row list itself changes only when ids or groups do.
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

// Selection
const selectMode = ref(false)
const selection = shallowRef(EMPTY_SELECTION)
watch(order, (value) => (selection.value = prune(selection.value, value)))
watch(selectMode, (on) => !on && (selection.value = EMPTY_SELECTION))
const selectedIds = computed(() => order.value.filter((id) => selection.value.ids.has(id)))

function onToggle(id: string, extend: boolean) {
  selectMode.value = true
  selection.value = extend
    ? extendTo(selection.value, order.value, id)
    : toggle(selection.value, id)
}

const eligible = computed(() => {
  const pick = (test: typeof canCancel) =>
    selectedIds.value.filter((id) => {
      const task = tasks.get(id)
      return !!task && test(task)
    })
  return { cancel: pick(canCancel), accept: pick(awaitsReview) }
})
const bulk = ref<BulkAction | null>(null)
function onBulkDone() {
  bulk.value = null
  selectMode.value = false
}

const unregister = useCommandsStore().register({
  id: 'tasks.select',
  title: { key: 'tasks.cmd.select' },
  group: 'tasks',
  icon: 'select',
  when: () => !selectMode.value,
  run: () => {
    selectMode.value = true
  },
})
onBeforeUnmount(() => {
  unregister()
  clearTimeout(qTimer)
})

// Keyboard: one row is in the tab order (roving); arrows walk the rows like a tree.
const list = ref<HTMLElement | null>(null)
const focusKey = ref<string | null>(null)
const tabStop = computed(() => {
  const keys = rows.value.map(rowKey)
  if (focusKey.value && keys.includes(focusKey.value)) return focusKey.value
  return keys[0] ?? null
})

function focusRow(key: string) {
  focusKey.value = key
  void nextTick(() =>
    list.value?.querySelector<HTMLElement>(`[data-key="${CSS.escape(key)}"]`)?.focus(),
  )
}

function onKeydown(event: KeyboardEvent) {
  if (event.isComposing || event.altKey) return
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'a') {
    event.preventDefault()
    selectMode.value = true
    selection.value = selectAll(order.value)
    return
  }
  if (event.key === 'Escape' && selectMode.value) {
    event.preventDefault()
    event.stopPropagation()
    selectMode.value = false
    return
  }
  const current = focusKey.value
  if (event.key === ' ' && selectMode.value && current?.startsWith('t:')) {
    event.preventDefault()
    onToggle(current.slice(2), event.shiftKey)
    return
  }
  if (event.ctrlKey || event.metaKey) return
  const action = listKey(rows.value, current, event.key)
  if (!action) return
  event.preventDefault()
  if ('focus' in action) focusRow(action.focus)
  else if ('collapse' in action) toggleGroup(action.collapse, true)
  else toggleGroup(action.expand, false)
}

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
        v-model="q"
        type="search"
        icon="search"
        size="sm"
        hide-label
        :label="t('tasks.list.search')"
        :placeholder="t('tasks.list.searchPlaceholder')"
      />
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
