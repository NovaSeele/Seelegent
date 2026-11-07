/**
 * Tasks: every known TaskSummary keyed by id, plus event logs of the tasks on screen.
 *
 * Granularity: summaries live in a shallowReactive Map. A component that reads `get(id)` depends on that one key,
 * so a task.updated frame re-renders only what shows that task; lists derive stable id arrays (lib/stable.ts) and
 * render rows that read their own task.
 *
 * Summaries arrive from GET /state (the boot snapshot), from SSE task.created/updated/deleted, and from paged
 * GET /tasks reads (older tasks, server-side filters). Paged reads never overwrite a newer summary.
 *
 * Event logs: a view retains a task's log while it is on screen (`retainEvents`), which loads the newest
 * TAIL events; task.event frames are appended in seq order. A frame whose seq skips ahead means frames were
 * missed: the log reads forward from its last seq (`after`) before applying it. Released logs are dropped after a
 * short grace period, so switching views does not reload them.
 */
import { defineStore } from 'pinia'
import { shallowReactive, shallowRef, triggerRef, type ShallowRef } from 'vue'

import { tasksApi as defaultApi } from '@/api'
import { createEventParser, type TaskEvent } from '@/api/events'
import type { TaskListQuery, TasksApi } from '@/api/tasks'
import type { TaskSummary } from '@/api/types'
import { nextFrame } from '@/lib/frame'
import { awaitsReview, isPending } from '@/lib/taskStatus'

export const TAIL = 500
export const PAGE = 500
export const LIST_PAGE = 50
/** Most events kept per task; older ones are dropped from memory (and can be loaded again). */
export const MAX_EVENTS = 20_000
export const RELEASE_GRACE_MS = 15_000
/** A summary saying the log has more than we hold triggers a catch-up read after this delay. */
export const CATCH_UP_DELAY_MS = 400

export interface EventLogState {
  status: 'loading' | 'ready' | 'error'
  error: string | null
  /** Seq of the first event held (1 = the log is complete from the start). */
  firstSeq: number
  /** Seq of the last event held. */
  lastSeq: number
  loadingEarlier: boolean
}

export interface EventLog {
  readonly taskId: string
  /** Ascending, contiguous seqs firstSeq..lastSeq. Replaced or mutated + triggered; read `.value`. */
  readonly items: ShallowRef<TaskEvent[]>
  readonly state: EventLogState
}

/** An event as received: only seq and taskId are trusted until the parser has checked it. */
type RawEvent = Pick<TaskEvent, 'seq' | 'taskId'>

const bySeq = <T extends RawEvent>(events: readonly T[]): T[] =>
  [...events].sort((a, b) => a.seq - b.seq)

interface LogInternal extends EventLog {
  refs: number
  pending: RawEvent[]
  syncing: boolean
  renderScheduled: boolean
  releaseTimer?: ReturnType<typeof setTimeout>
  catchUpTimer?: ReturnType<typeof setTimeout>
}

export interface ListPaging {
  key: string
  cursor: string | undefined
  done: boolean
  loading: boolean
  error: string | null
}

function newer(next: TaskSummary, current: TaskSummary | undefined): boolean {
  if (!current) return true
  const a = Date.parse(next.updatedAt)
  const b = Date.parse(current.updatedAt)
  if (a !== b) return a > b
  return next.lastEventSeq >= current.lastEventSeq
}

/** Tasks GET /state always includes, whatever their age (contract §2). */
const alwaysInSnapshot = (task: TaskSummary) =>
  isPending(task) || task.attention !== null || awaitsReview(task)

const messageOf = (error: unknown) => (error instanceof Error ? error.message : String(error))

export const useTasksStore = defineStore('tasks', () => {
  let client: TasksApi = defaultApi
  const parse = createEventParser()
  const summaries = shallowReactive(new Map<string, TaskSummary>())
  const logs = new Map<string, LogInternal>()
  const paging = shallowReactive<ListPaging>({
    key: '',
    cursor: undefined,
    done: false,
    loading: false,
    error: null,
  })
  /** True once a boot snapshot has been applied. */
  const loaded = shallowRef(false)

  function setApi(next: TasksApi): void {
    client = next
  }

  function get(id: string): TaskSummary | undefined {
    return summaries.get(id)
  }

  /** Every summary (depends on all of them: use in computeds that settle into stable results). */
  function all(): TaskSummary[] {
    return [...summaries.values()]
  }

  function upsert(task: TaskSummary): void {
    // A verifier's review task is part of its parent's gate, never a task of its own (docs/gates.md).
    if (task.kind === 'verify') return
    summaries.set(task.id, task)
    const log = logs.get(task.id)
    if (log && task.lastEventSeq > log.state.lastSeq) scheduleCatchUp(log)
  }

  function upsertIfNewer(task: TaskSummary): void {
    if (newer(task, summaries.get(task.id))) upsert(task)
  }

  function remove(id: string): void {
    summaries.delete(id)
    const log = logs.get(id)
    if (log) dropLog(log)
  }

  /**
   * Applies a /state snapshot: every pending task, every task with attention, every done task without a review,
   * plus the 100 newest others. A known task missing from it is gone, unless it is one of the "others" older than
   * the snapshot's oldest "other" (then it is only outside the snapshot's window).
   */
  function applySnapshot(tasks: readonly TaskSummary[]): void {
    const ids = new Set(tasks.map((task) => task.id))
    // The "100 newest others" set the window; tasks the snapshot always carries do not.
    const others = tasks.filter((task) => !alwaysInSnapshot(task))
    const horizon = others.length
      ? Math.min(...others.map((task) => Date.parse(task.createdAt)))
      : Infinity
    for (const [id, task] of summaries) {
      if (ids.has(id)) continue
      if (alwaysInSnapshot(task) || Date.parse(task.createdAt) >= horizon) remove(id)
    }
    for (const task of tasks) upsert(task)
    loaded.value = true
    for (const log of logs.values()) {
      const task = summaries.get(log.taskId)
      if (!task) continue
      if (task.lastEventSeq < log.state.lastSeq) void loadTail(log)
      else if (task.lastEventSeq > log.state.lastSeq) void catchUp(log)
    }
  }

  // Paged list reads.

  function pagingKey(query: TaskListQuery): string {
    return JSON.stringify({
      status: query.status ?? [],
      project: query.project ?? '',
      q: query.q?.trim() ?? '',
    })
  }

  /** Whether `query` may have tasks beyond those already read for it. */
  function hasMore(query: TaskListQuery): boolean {
    return paging.key !== pagingKey(query) || !paging.done
  }

  /** Reads the next page of `query` (the first page when the query changed) into the store. */
  async function loadPage(query: TaskListQuery): Promise<void> {
    const key = pagingKey(query)
    if (paging.key !== key) {
      Object.assign(paging, { key, cursor: undefined, done: false, loading: false, error: null })
    }
    if (paging.done || paging.loading) return
    paging.loading = true
    paging.error = null
    try {
      const page = await client.list({ ...query, limit: LIST_PAGE, cursor: paging.cursor })
      if (paging.key !== key) return
      for (const task of page.items) upsertIfNewer(task)
      paging.cursor = page.nextCursor
      paging.done = !page.nextCursor
    } catch (error) {
      if (paging.key === key) paging.error = messageOf(error)
    } finally {
      if (paging.key === key) paging.loading = false
    }
  }

  // Event logs.

  function retainEvents(taskId: string): EventLog {
    let log = logs.get(taskId)
    if (!log) {
      log = {
        taskId,
        items: shallowRef<TaskEvent[]>([]),
        state: shallowReactive<EventLogState>({
          status: 'loading',
          error: null,
          firstSeq: 1,
          lastSeq: 0,
          loadingEarlier: false,
        }),
        refs: 0,
        pending: [],
        syncing: false,
        renderScheduled: false,
      }
      logs.set(taskId, log)
      void loadTail(log)
    }
    clearTimeout(log.releaseTimer)
    log.releaseTimer = undefined
    log.refs++
    return log
  }

  function releaseEvents(taskId: string): void {
    const log = logs.get(taskId)
    if (!log || log.refs === 0) return
    log.refs--
    if (log.refs > 0) return
    log.releaseTimer = setTimeout(() => {
      if (log.refs === 0) dropLog(log)
    }, RELEASE_GRACE_MS)
  }

  function dropLog(log: LogInternal): void {
    clearTimeout(log.releaseTimer)
    clearTimeout(log.catchUpTimer)
    logs.delete(log.taskId)
  }

  /** The log of a task while some view retains it. */
  function eventLog(taskId: string): EventLog | undefined {
    return logs.get(taskId)
  }

  async function loadTail(log: LogInternal): Promise<void> {
    log.state.status = 'loading'
    log.state.error = null
    log.syncing = true
    try {
      const page = await client.events(log.taskId, { tail: TAIL })
      if (logs.get(log.taskId) !== log) return
      const raw = bySeq(page.items)
      log.items.value = raw.map(parse).filter((event): event is TaskEvent => event !== null)
      log.state.firstSeq = raw[0]?.seq ?? page.lastSeq + 1
      log.state.lastSeq = raw.at(-1)?.seq ?? page.lastSeq
      log.state.status = 'ready'
      log.syncing = false
      drainPending(log)
    } catch (error) {
      log.syncing = false
      if (logs.get(log.taskId) !== log) return
      log.state.status = 'error'
      log.state.error = messageOf(error)
    }
  }

  /**
   * Appends events that continue the log. An event the parser rejects (an unknown type from a newer daemon)
   * still advances lastSeq, so it never looks like a gap. Views re-render once per animation frame.
   */
  function append(log: LogInternal, events: readonly RawEvent[]): void {
    let added = false
    for (const raw of events) {
      if (raw.seq !== log.state.lastSeq + 1) continue
      log.state.lastSeq = raw.seq
      const event = parse(raw)
      if (!event) continue
      log.items.value.push(event)
      added = true
    }
    if (!added) return
    const overflow = log.items.value.length - MAX_EVENTS
    if (overflow > 0) {
      log.items.value.splice(0, overflow)
      log.state.firstSeq = log.items.value[0]?.seq ?? log.state.lastSeq + 1
    }
    scheduleRender(log)
  }

  function scheduleRender(log: LogInternal): void {
    if (log.renderScheduled) return
    log.renderScheduled = true
    nextFrame(() => {
      log.renderScheduled = false
      triggerRef(log.items)
    })
  }

  /** Applies buffered frames in order; a remaining gap starts a catch-up read. */
  function drainPending(log: LogInternal): void {
    if (log.pending.length === 0) return
    const pending = log.pending.sort((a, b) => a.seq - b.seq)
    log.pending = []
    const fresh = pending.filter((event) => event.seq > log.state.lastSeq)
    const first = fresh[0]
    if (!first) return
    if (first.seq !== log.state.lastSeq + 1) {
      log.pending = fresh
      void catchUp(log)
      return
    }
    let contiguous = 0
    while (
      contiguous < fresh.length &&
      fresh[contiguous]?.seq === log.state.lastSeq + 1 + contiguous
    )
      contiguous++
    append(log, fresh.slice(0, contiguous))
    if (contiguous < fresh.length) {
      log.pending = fresh.slice(contiguous)
      void catchUp(log)
    }
  }

  /**
   * Reads forward from the last held seq until the log is current, then applies buffered frames. A read that
   * does not move lastSeq although there is more (the server's page skips seqs, or held frames stay out of reach)
   * would repeat forever: the log reloads its tail instead and drops the held frames (the tail covers them).
   */
  async function catchUp(log: LogInternal): Promise<void> {
    if (log.syncing || log.state.status !== 'ready') return
    log.syncing = true
    try {
      let stuck = false
      for (;;) {
        const before = log.state.lastSeq
        const page = await client.events(log.taskId, { after: before, limit: PAGE })
        if (logs.get(log.taskId) !== log) return
        append(log, bySeq(page.items))
        if (log.state.lastSeq === before) {
          stuck = page.items.length > 0 || log.pending.some((event) => event.seq > before)
          break
        }
        if (page.items.length < PAGE || log.state.lastSeq >= page.lastSeq) break
      }
      log.syncing = false
      if (stuck) {
        log.pending = []
        void loadTail(log)
        return
      }
      drainPending(log)
    } catch (error) {
      log.syncing = false
      log.state.error = messageOf(error)
    }
  }

  function scheduleCatchUp(log: LogInternal): void {
    if (log.catchUpTimer) return
    log.catchUpTimer = setTimeout(() => {
      log.catchUpTimer = undefined
      const task = summaries.get(log.taskId)
      if (task && task.lastEventSeq > log.state.lastSeq) void catchUp(log)
    }, CATCH_UP_DELAY_MS)
  }

  /** A task.event frame. Only logs on screen keep events; others are told by lastEventSeq later. */
  function applyEvent(event: RawEvent): void {
    if (typeof event?.taskId !== 'string' || !Number.isInteger(event.seq)) return
    const log = logs.get(event.taskId)
    if (!log) return
    if (log.syncing || log.state.status !== 'ready') {
      log.pending.push(event)
      return
    }
    if (event.seq <= log.state.lastSeq) return
    if (event.seq === log.state.lastSeq + 1) {
      append(log, [event])
      return
    }
    log.pending.push(event)
    void catchUp(log)
  }

  /** Loads up to PAGE events before the first one held. */
  async function loadEarlier(taskId: string): Promise<void> {
    const log = logs.get(taskId)
    if (!log || log.state.firstSeq <= 1 || log.state.loadingEarlier) return
    const before = log.state.firstSeq
    const after = Math.max(0, before - 1 - PAGE)
    log.state.loadingEarlier = true
    try {
      const page = await client.events(taskId, { after, limit: before - 1 - after })
      if (logs.get(taskId) !== log || log.state.firstSeq !== before) return
      const older = bySeq(page.items.filter((event) => event.seq < before))
      if (older.length) {
        const parsed = older.map(parse).filter((event): event is TaskEvent => event !== null)
        log.items.value = [...parsed, ...log.items.value]
        log.state.firstSeq = older[0]?.seq ?? before
      }
    } catch (error) {
      log.state.error = messageOf(error)
    } finally {
      log.state.loadingEarlier = false
    }
  }

  function retryEvents(taskId: string): void {
    const log = logs.get(taskId)
    if (log) void loadTail(log)
  }

  return {
    summaries,
    paging,
    loaded,
    setApi,
    get,
    all,
    upsert,
    upsertIfNewer,
    remove,
    applySnapshot,
    hasMore,
    loadPage,
    retainEvents,
    releaseEvents,
    eventLog,
    applyEvent,
    loadEarlier,
    retryEvents,
  }
})

export type TasksStore = ReturnType<typeof useTasksStore>
