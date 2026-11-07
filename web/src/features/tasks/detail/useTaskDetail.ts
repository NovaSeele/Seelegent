/**
 * The open task: its live summary from the store and its full record (turns, brief, error) from
 * GET /tasks/{id}. The record is read again when the summary says it moved on (a new turn, a status change), at
 * most once per second; answers for a task that is no longer open are dropped.
 */
import { computed, onScopeDispose, shallowRef, watch } from 'vue'

import { tasksApi, type TasksApi } from '@/api'
import type { TaskDetail, TaskSummary } from '@/api/types'
import { useTasksStore } from '@/stores/tasks'

const REFETCH_GAP_MS = 1000

/** The summary part of a detail record (what the store keeps). */
export function summaryOf(detail: TaskDetail): TaskSummary {
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  const { prompt, repoRoot, error, turns, fileOverlaps, ...summary } = detail
  return summary
}

/** What makes the record stale: the parts of a summary that change when turns or the outcome change. */
function revision(task: TaskSummary | undefined): string {
  if (!task) return ''
  return [
    task.status,
    task.turnCount,
    task.review?.verdict ?? '',
    task.manual,
    task.filesChanged,
  ].join('|')
}

export function useTaskDetail(taskId: () => string, api: TasksApi = tasksApi) {
  const tasks = useTasksStore()
  const detail = shallowRef<TaskDetail | null>(null)
  const error = shallowRef<string | null>(null)
  const loading = shallowRef(false)
  let request = 0
  let lastFetch = 0
  let timer: ReturnType<typeof setTimeout> | undefined

  const summary = computed<TaskSummary | undefined>(() => {
    const live = tasks.get(taskId())
    if (live) return live
    const record = detail.value
    return record && record.id === taskId() ? record : undefined
  })

  async function load() {
    clearTimeout(timer)
    timer = undefined
    const id = taskId()
    const mine = ++request
    loading.value = true
    lastFetch = Date.now()
    try {
      const record = await api.get(id)
      if (mine !== request) return
      detail.value = record
      error.value = null
      tasks.upsertIfNewer(summaryOf(record))
    } catch (err) {
      if (mine !== request) return
      error.value = err instanceof Error ? err.message : String(err)
    } finally {
      if (mine === request) loading.value = false
    }
  }

  function schedule() {
    if (timer !== undefined) return
    const wait = Math.max(0, lastFetch + REFETCH_GAP_MS - Date.now())
    timer = setTimeout(() => void load(), wait)
  }

  watch(
    taskId,
    () => {
      detail.value = null
      error.value = null
      void load()
    },
    { immediate: true },
  )

  watch(
    () => revision(tasks.get(taskId())),
    (next, previous) => {
      if (previous && next !== previous && detail.value) schedule()
    },
  )

  onScopeDispose(() => {
    request++
    clearTimeout(timer)
  })

  return { summary, detail, error, loading, reload: load }
}
