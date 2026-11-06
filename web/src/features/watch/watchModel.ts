/**
 * The Watch board as plain data: who needs you (most urgent kind first, longest waiting first), what is running,
 * and what finished recently. The board holds ids, not tasks, so cards read their own task from the store and a
 * task update re-renders only its card; boards are compared with `sameBoard` to keep the lists themselves stable.
 */
import type { TaskStatus, TaskSummary } from '@/api/types'
import { isVisibleTask, NEED_ORDER, needKind, waitingSince, type NeedKind } from '@/lib/taskStatus'
import { isBoolean, isOneOf, type StoredValue } from '@/lib/storage'

export const WATCH_TABS = ['all', 'needs', 'running'] as const
export type WatchTab = (typeof WATCH_TABS)[number]

export const WATCH_TAB: StoredValue<WatchTab> = {
  name: 'watch.tab',
  fallback: 'all',
  validate: isOneOf(WATCH_TABS),
}

/** Whether the account strip is folded away (its own key, never shared). */
export const WATCH_ACCOUNTS_OPEN: StoredValue<boolean> = {
  name: 'watch.accountsOpen',
  fallback: true,
  validate: isBoolean,
}

export const RECENT_MAX = 8

export interface NeedEntry {
  id: string
  kind: NeedKind
  since: string
}

export interface WatchBoard {
  needs: NeedEntry[]
  running: string[]
  recent: string[]
  queued: number
}

const RUNNING_ORDER: readonly TaskStatus[] = ['running', 'waiting_quota', 'queued', 'waiting_deps']

const time = (iso: string) => Date.parse(iso) || 0

export function buildBoard(
  tasks: Iterable<TaskSummary>,
  now: number,
  recentMax = RECENT_MAX,
): WatchBoard {
  const needs: NeedEntry[] = []
  const running: TaskSummary[] = []
  const finished: TaskSummary[] = []
  let queued = 0
  for (const task of tasks) {
    if (!isVisibleTask(task)) continue
    const kind = needKind(task, now)
    if (kind) {
      needs.push({ id: task.id, kind, since: waitingSince(task) })
      continue
    }
    if (RUNNING_ORDER.includes(task.status)) {
      running.push(task)
      if (task.status === 'queued') queued++
    } else finished.push(task)
  }
  needs.sort(
    (a, b) =>
      NEED_ORDER.indexOf(a.kind) - NEED_ORDER.indexOf(b.kind) ||
      time(a.since) - time(b.since) ||
      a.id.localeCompare(b.id),
  )
  running.sort(
    (a, b) =>
      RUNNING_ORDER.indexOf(a.status) - RUNNING_ORDER.indexOf(b.status) ||
      time(b.createdAt) - time(a.createdAt) ||
      a.id.localeCompare(b.id),
  )
  finished.sort((a, b) => time(b.updatedAt) - time(a.updatedAt) || a.id.localeCompare(b.id))
  return {
    needs,
    running: running.map((task) => task.id),
    recent: finished.slice(0, recentMax).map((task) => task.id),
    queued,
  }
}

const sameIds = (a: readonly string[], b: readonly string[]) =>
  a.length === b.length && a.every((id, i) => id === b[i])

export function sameNeeds(a: readonly NeedEntry[], b: readonly NeedEntry[]): boolean {
  return (
    a.length === b.length &&
    a.every((n, i) => n.id === b[i]?.id && n.kind === b[i]?.kind && n.since === b[i]?.since)
  )
}

export function sameBoard(a: WatchBoard, b: WatchBoard): boolean {
  return (
    a.queued === b.queued &&
    sameNeeds(a.needs, b.needs) &&
    sameIds(a.running, b.running) &&
    sameIds(a.recent, b.recent)
  )
}

/** Tone and icon of each need kind: failures are danger, everything else waits on you (attention). */
export const NEED_LOOK = {
  question: { tone: 'attention', icon: 'question' },
  blocked: { tone: 'attention', icon: 'statusBlocked' },
  stalled: { tone: 'attention', icon: 'statusWaiting' },
  failed: { tone: 'danger', icon: 'statusFailed' },
  depsBlocked: { tone: 'danger', icon: 'statusBlocked' },
  review: { tone: 'attention', icon: 'statusReview' },
} as const

/** The main next step on a need card. */
export function nextStep(kind: NeedKind): 'answer' | 'review' | 'open' {
  if (kind === 'question') return 'answer'
  if (kind === 'review') return 'review'
  return 'open'
}

/** Waiting longer than this turns the age label into the overdue tone. */
export const OVERDUE_MS = 24 * 3600_000
