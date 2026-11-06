/**
 * The task list as plain data: which tasks match the search and filters, how they group (by project, with a
 * pinned "needs you" group on top, or flat), and the flat row sequence the list renders and the keyboard walks.
 * Rows hold ids only; each row component reads its task from the store, so one task update re-renders one row.
 */
import type { TaskListQuery } from '@/api/tasks'
import type { TaskStatus, TaskSummary } from '@/api/types'
import {
  awaitsReview,
  isPending,
  isVisibleTask,
  needKind,
  PENDING_STATUSES,
} from '@/lib/taskStatus'
import { isOneOf, type StoredValue } from '@/lib/storage'

export const STATUS_FILTERS = ['needs', 'active', 'review', 'done', 'failed', 'cancelled'] as const
export type StatusFilter = (typeof STATUS_FILTERS)[number]

export const GROUPINGS = ['project', 'flat'] as const
export type Grouping = (typeof GROUPINGS)[number]

export interface ListFilters {
  q: string
  statuses: readonly StatusFilter[]
  /** Project key, '' for every project. */
  project: string
}

export const NEEDS_GROUP = '@needs'

export type ListRow =
  | { kind: 'group'; key: string; label: string; count: number; collapsed: boolean }
  | { kind: 'task'; id: string; group: string }

// Stored preferences, each under its own key (the old dashboard shared one key between two features: bug B1).
export const LIST_GROUPING: StoredValue<Grouping> = {
  name: 'tasks.grouping',
  fallback: 'project',
  validate: isOneOf(GROUPINGS),
}

export const COLLAPSED_GROUPS: StoredValue<string[]> = {
  name: 'tasks.collapsedGroups',
  fallback: [],
  validate: (value): value is string[] =>
    Array.isArray(value) && value.length <= 500 && value.every((v) => typeof v === 'string'),
}

export const STATUS_FILTER: StoredValue<StatusFilter[]> = {
  name: 'tasks.statusFilter',
  fallback: [],
  validate: (value): value is StatusFilter[] =>
    Array.isArray(value) && value.every(isOneOf(STATUS_FILTERS)),
}

/** Lower-cased and without diacritics, so "loi" finds "lỗi" and "cafe" finds "café". */
export function fold(text: string): string {
  return text
    .normalize('NFD')
    .replace(/\p{M}/gu, '')
    .replace(/đ/g, 'd')
    .replace(/Đ/g, 'D')
    .toLowerCase()
}

export function matchesStatus(task: TaskSummary, filter: StatusFilter, now: number): boolean {
  switch (filter) {
    case 'needs':
      return needKind(task, now) !== null
    case 'active':
      return isPending(task) && needKind(task, now) === null
    case 'review':
      return awaitsReview(task)
    case 'done':
      return task.status === 'done' && !!task.review
    case 'failed':
      return task.status === 'failed' || task.status === 'blocked_deps'
    case 'cancelled':
      return task.status === 'cancelled'
  }
}

export function matches(task: TaskSummary, filters: ListFilters, now: number): boolean {
  if (!isVisibleTask(task)) return false
  if (filters.project && task.projectKey !== filters.project) return false
  if (filters.statuses.length && !filters.statuses.some((f) => matchesStatus(task, f, now))) {
    return false
  }
  const q = fold(filters.q.trim())
  if (!q) return true
  return q
    .split(/\s+/)
    .every((word) =>
      [task.title, task.id, task.projectName, task.accountLabel ?? ''].some((field) =>
        fold(field).includes(word),
      ),
    )
}

const SERVER_STATUSES: Record<StatusFilter, readonly TaskStatus[] | null> = {
  needs: null,
  active: PENDING_STATUSES,
  review: ['done'],
  done: ['done'],
  failed: ['failed', 'blocked_deps'],
  cancelled: ['cancelled'],
}

/** The GET /tasks query that pages in older tasks for these filters (a superset; matches() narrows it). */
export function apiQuery(filters: ListFilters): TaskListQuery {
  const query: TaskListQuery = {}
  const sets = filters.statuses.map((f) => SERVER_STATUSES[f])
  if (sets.length && sets.every((set) => set !== null)) {
    query.status = [...new Set(sets.flatMap((set) => set ?? []))].sort()
  }
  if (filters.project) query.project = filters.project
  if (filters.q.trim()) query.q = filters.q.trim()
  return query
}

const time = (iso: string) => Date.parse(iso) || 0
const newestFirst = (a: TaskSummary, b: TaskSummary) =>
  time(b.createdAt) - time(a.createdAt) || (b.id < a.id ? -1 : 1)

export function buildRows(
  tasks: Iterable<TaskSummary>,
  filters: ListFilters,
  grouping: Grouping,
  collapsed: ReadonlySet<string>,
  now: number,
): ListRow[] {
  const list = [...tasks].filter((task) => matches(task, filters, now)).sort(newestFirst)
  if (grouping === 'flat') return list.map((task) => ({ kind: 'task', id: task.id, group: '' }))

  const needs: TaskSummary[] = []
  const groups = new Map<string, { label: string; tasks: TaskSummary[]; last: number }>()
  for (const task of list) {
    if (needKind(task, now)) {
      needs.push(task)
      continue
    }
    const group = groups.get(task.projectKey) ?? { label: task.projectName, tasks: [], last: 0 }
    group.tasks.push(task)
    group.last = Math.max(group.last, time(task.updatedAt))
    groups.set(task.projectKey, group)
  }
  const rows: ListRow[] = []
  const push = (key: string, label: string, members: TaskSummary[]) => {
    const isCollapsed = collapsed.has(key)
    rows.push({ kind: 'group', key, label, count: members.length, collapsed: isCollapsed })
    if (!isCollapsed)
      for (const task of members) rows.push({ kind: 'task', id: task.id, group: key })
  }
  if (needs.length) push(NEEDS_GROUP, '', needs)
  const ordered = [...groups.entries()].sort(
    ([ka, a], [kb, b]) => b.last - a.last || ka.localeCompare(kb),
  )
  for (const [key, group] of ordered) push(key, group.label, group.tasks)
  return rows
}

export type ListKeyAction = { focus: string } | { collapse: string } | { expand: string } | null

/**
 * Keyboard walk of the list (tree-like): Up/Down/Home/End move, Left collapses a group or goes from a task to
 * its group, Right expands a group or steps into it. `current` is a rowKey; null starts at the first row.
 */
export function listKey(
  rows: readonly ListRow[],
  current: string | null,
  key: string,
): ListKeyAction {
  if (!rows.length) return null
  const keys = rows.map(rowKey)
  const index = current ? keys.indexOf(current) : -1
  const row = index >= 0 ? rows[index] : undefined
  const at = (i: number) => {
    const target = keys[Math.min(keys.length - 1, Math.max(0, i))]
    return target ? { focus: target } : null
  }
  switch (key) {
    case 'ArrowDown':
      return at(index + 1)
    case 'ArrowUp':
      return at(index < 0 ? 0 : index - 1)
    case 'Home':
      return at(0)
    case 'End':
      return at(keys.length - 1)
    case 'ArrowLeft':
      if (row?.kind === 'group') return row.collapsed ? null : { collapse: row.key }
      if (row?.kind === 'task' && row.group) return { focus: `g:${row.group}` }
      return null
    case 'ArrowRight':
      if (row?.kind !== 'group') return null
      return row.collapsed ? { expand: row.key } : at(index + 1)
    default:
      return null
  }
}

export const rowKey = (row: ListRow) => (row.kind === 'group' ? `g:${row.key}` : `t:${row.id}`)

export function sameRows(a: readonly ListRow[], b: readonly ListRow[]): boolean {
  if (a.length !== b.length) return false
  return a.every((row, i) => {
    const other = b[i]
    if (!other || other.kind !== row.kind) return false
    if (row.kind === 'task' && other.kind === 'task') {
      return row.id === other.id && row.group === other.group
    }
    if (row.kind === 'group' && other.kind === 'group') {
      return (
        row.key === other.key &&
        row.label === other.label &&
        row.count === other.count &&
        row.collapsed === other.collapsed
      )
    }
    return false
  })
}
