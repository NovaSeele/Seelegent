/**
 * The process model of one task for the Tree, Graph and Files views: retains the task's event log while the
 * view is mounted, folds it into timeline items (reusing unchanged ones) and builds turns, attempts and steps.
 */
import { computed, onBeforeUnmount } from 'vue'

import type { TaskDetail } from '@/api/types'
import { useTasksStore } from '@/stores/tasks'

import { buildTimeline, sameTimeline, type TimelineItem } from '../terminal/timeline'
import { allSteps, buildProcess, type StepCache } from './process'

export function useProcess(taskId: string, detail: () => TaskDetail | null) {
  const tasks = useTasksStore()
  const log = tasks.retainEvents(taskId)
  onBeforeUnmount(() => tasks.releaseEvents(taskId))

  let previous: TimelineItem[] = []
  const items = computed(() => {
    const next = buildTimeline(log.items.value, previous)
    if (sameTimeline(previous, next)) return previous
    previous = next
    return next
  })
  const cache: StepCache = new WeakMap()
  const turns = computed(() => buildProcess(items.value, detail()?.turns, cache))
  const steps = computed(() => allSteps(turns.value))
  const partial = computed(() => log.state.status === 'ready' && log.state.firstSeq > 1)
  /** The oldest turn the loaded part of the log reaches (Infinity when it reaches none). */
  const firstLoadedTurn = computed(() => {
    let first = Infinity
    for (const item of items.value) if (item.turn && item.turn < first) first = item.turn
    return first
  })

  return {
    state: log.state,
    turns,
    steps,
    /** Older events exist that the log does not hold yet. */
    partial,
    /** True for a turn whose events all lie before the loaded part of the log: its steps are unknown, not zero. */
    notLoaded: (turn: number) => partial.value && turn < firstLoadedTurn.value,
    loadEarlier: () => tasks.loadEarlier(taskId),
    retry: () => tasks.retryEvents(taskId),
  }
}

export type ProcessModel = ReturnType<typeof useProcess>
