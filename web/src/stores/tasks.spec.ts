import { flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { computed, effectScope } from 'vue'

import type { EventsQuery, TaskEventsPage } from '@/api/tasks'
import type { TaskEvent } from '@/api/types'
import { setupApp } from '@/test/app'
import { fakeTasksApi, iso, makeEvent, makeTask } from '@/test/fixtures'

import {
  CATCH_UP_DELAY_MS,
  PAGE,
  RELEASE_GRACE_MS,
  TAIL,
  useTasksStore,
  type EventLog,
} from './tasks'

/** A task's event log on the "server": answers tail and after/limit reads like the daemon. */
function serverLog(taskId: string, count: number) {
  const events: TaskEvent[] = []
  const add = (n: number) => {
    for (let i = 0; i < n; i++) events.push(makeEvent(taskId, events.length + 1))
  }
  add(count)
  const read = vi.fn(async (_id: string, query: EventsQuery): Promise<TaskEventsPage> => {
    const lastSeq = events.length
    if ('tail' in query) return { items: events.slice(-query.tail), lastSeq }
    const limit = query.limit ?? 500
    return { items: events.filter((e) => e.seq > query.after).slice(0, limit), lastSeq }
  })
  return { events, add, read }
}

const seqs = (log: EventLog) => log.items.value.map((e) => e.seq)

describe('tasks store: summaries', () => {
  beforeEach(async () => {
    await setupApp()
  })

  it('updates one task without invalidating readers of another', () => {
    const store = useTasksStore()
    store.upsert(makeTask({ id: 'a' }))
    store.upsert(makeTask({ id: 'b' }))
    const scope = effectScope()
    let runsA = 0
    let runsB = 0
    const a = scope.run(() => computed(() => (runsA++, store.get('a')?.title)))
    const b = scope.run(() => computed(() => (runsB++, store.get('b')?.title)))
    expect([a?.value, b?.value]).toEqual(['Fix the login form', 'Fix the login form'])
    store.upsert(makeTask({ id: 'b', title: 'Renamed' }))
    expect([a?.value, b?.value]).toEqual(['Fix the login form', 'Renamed'])
    expect([runsA, runsB]).toEqual([1, 2])
    scope.stop()
  })

  it('removes tasks missing from a snapshot but keeps older ones outside its window', () => {
    const store = useTasksStore()
    store.upsert(makeTask({ id: 'old', status: 'done', createdAt: iso(-86400_000) }))
    store.upsert(makeTask({ id: 'gone', status: 'done', createdAt: iso(1000) }))
    store.upsert(makeTask({ id: 'gonePending', status: 'queued', createdAt: iso(-90000_000) }))
    store.applySnapshot([
      makeTask({ id: 'keep', status: 'done', createdAt: iso() }),
      makeTask({ id: 'run', status: 'running', createdAt: iso(5000) }),
    ])
    expect([...store.summaries.keys()].sort()).toEqual(['keep', 'old', 'run'])
    expect(store.loaded).toBe(true)
  })

  it('pages through GET /tasks per query and never overwrites a newer summary', async () => {
    const store = useTasksStore()
    store.upsert(makeTask({ id: 'a', title: 'live', updatedAt: iso(5000) }))
    const list = vi
      .fn()
      .mockResolvedValueOnce({
        items: [makeTask({ id: 'a', title: 'stale', updatedAt: iso(1000) }), makeTask({ id: 'b' })],
        nextCursor: 'c2',
      })
      .mockResolvedValueOnce({ items: [makeTask({ id: 'c' })] })
      .mockResolvedValueOnce({ items: [] })
    store.setApi(fakeTasksApi({ list }))
    const query = { status: ['done' as const], q: 'x' }
    expect(store.hasMore(query)).toBe(true)
    await store.loadPage(query)
    expect(list).toHaveBeenLastCalledWith({ ...query, limit: 50, cursor: undefined })
    expect(store.get('a')?.title).toBe('live')
    await store.loadPage(query)
    expect(list).toHaveBeenLastCalledWith({ ...query, limit: 50, cursor: 'c2' })
    expect(store.hasMore(query)).toBe(false)
    await store.loadPage(query)
    expect(list).toHaveBeenCalledTimes(2)
    // Another query starts again from its first page.
    expect(store.hasMore({ q: 'y' })).toBe(true)
    await store.loadPage({ q: 'y' })
    expect(list).toHaveBeenLastCalledWith({ q: 'y', limit: 50, cursor: undefined })
    expect([...store.summaries.keys()].sort()).toEqual(['a', 'b', 'c'])
  })
})

describe('tasks store: event logs', () => {
  beforeEach(async () => {
    await setupApp()
    vi.useFakeTimers()
  })
  afterEach(() => vi.useRealTimers())

  function setup(count: number) {
    const server = serverLog('t1', count)
    const store = useTasksStore()
    store.setApi(fakeTasksApi({ events: server.read }))
    store.upsert(makeTask({ id: 't1', lastEventSeq: count }))
    return { store, server }
  }

  it('loads the newest events, then appends frames in seq order and ignores duplicates', async () => {
    const { store, server } = setup(800)
    const log = store.retainEvents('t1')
    expect(log.state.status).toBe('loading')
    await flushPromises()
    expect(server.read).toHaveBeenCalledWith('t1', { tail: TAIL })
    expect(log.state).toMatchObject({ status: 'ready', firstSeq: 301, lastSeq: 800 })
    server.add(1)
    store.applyEvent(makeEvent('t1', 801))
    store.applyEvent(makeEvent('t1', 801))
    store.applyEvent(makeEvent('t1', 799))
    expect(seqs(log).slice(-2)).toEqual([800, 801])
    expect(log.items.value).toHaveLength(501)
    expect(server.read).toHaveBeenCalledTimes(1)
  })

  it('holds frames that arrive while the tail loads and applies the new ones after it', async () => {
    const { store, server } = setup(10)
    const log = store.retainEvents('t1')
    server.add(2)
    store.applyEvent(makeEvent('t1', 12))
    store.applyEvent(makeEvent('t1', 11))
    store.applyEvent(makeEvent('t1', 9))
    await flushPromises()
    // The tail held 1..10; the held 11 and 12 follow it in order and the old 9 is ignored.
    expect(seqs(log)).toEqual(Array.from({ length: 12 }, (_, i) => i + 1))
  })

  it('reads forward from its last seq when a frame skips ahead, then applies the held frame', async () => {
    const { store, server } = setup(5)
    const log = store.retainEvents('t1')
    await flushPromises()
    server.add(3) // 6, 7, 8 happened; only 8 reaches us
    store.applyEvent(makeEvent('t1', 8))
    expect(log.state.lastSeq).toBe(5)
    await flushPromises()
    expect(server.read).toHaveBeenLastCalledWith('t1', { after: 5, limit: PAGE })
    expect(seqs(log)).toEqual([1, 2, 3, 4, 5, 6, 7, 8])
    server.add(1)
    store.applyEvent(makeEvent('t1', 9))
    expect(log.state.lastSeq).toBe(9)
  })

  it('pages a long catch-up until the log is current', async () => {
    const { store, server } = setup(1)
    const log = store.retainEvents('t1')
    await flushPromises()
    server.add(PAGE + 20)
    store.applyEvent(makeEvent('t1', PAGE + 21))
    await flushPromises()
    expect(log.state.lastSeq).toBe(PAGE + 21)
    expect(server.read.mock.calls.slice(1).map((c) => c[1])).toEqual([
      { after: 1, limit: PAGE },
      { after: PAGE + 1, limit: PAGE },
    ])
  })

  it('catches up when a summary says the log grew without frames (after a reset)', async () => {
    const { store, server } = setup(3)
    const log = store.retainEvents('t1')
    await flushPromises()
    server.add(2)
    store.upsert(makeTask({ id: 't1', lastEventSeq: 5 }))
    await vi.advanceTimersByTimeAsync(CATCH_UP_DELAY_MS)
    expect(log.state.lastSeq).toBe(5)
    server.add(1)
    store.applySnapshot([makeTask({ id: 't1', lastEventSeq: 6 })])
    await flushPromises()
    expect(log.state.lastSeq).toBe(6)
  })

  it('loads earlier events before the first one held', async () => {
    const { store } = setup(TAIL + 30)
    const log = store.retainEvents('t1')
    await flushPromises()
    expect(log.state.firstSeq).toBe(31)
    await store.loadEarlier('t1')
    expect(log.state.firstSeq).toBe(1)
    expect(seqs(log).slice(0, 3)).toEqual([1, 2, 3])
    expect(log.items.value).toHaveLength(TAIL + 30)
  })

  it('keeps a released log for a grace period, then drops it', async () => {
    const { store, server } = setup(3)
    store.retainEvents('t1')
    await flushPromises()
    store.releaseEvents('t1')
    await vi.advanceTimersByTimeAsync(RELEASE_GRACE_MS - 1)
    const again = store.retainEvents('t1')
    expect(server.read).toHaveBeenCalledTimes(1)
    store.releaseEvents('t1')
    await vi.advanceTimersByTimeAsync(RELEASE_GRACE_MS)
    expect(store.eventLog('t1')).toBeUndefined()
    store.applyEvent(makeEvent('t1', 4)) // nobody watches: ignored
    expect(again.state.lastSeq).toBe(3)
  })

  it('shows a load error and retries', async () => {
    const store = useTasksStore()
    const events = vi
      .fn()
      .mockRejectedValueOnce(new Error('down'))
      .mockResolvedValueOnce({ items: [makeEvent('t1', 1)], lastSeq: 1 })
    store.setApi(fakeTasksApi({ events }))
    const log = store.retainEvents('t1')
    await flushPromises()
    expect(log.state).toMatchObject({ status: 'error', error: 'down' })
    store.retryEvents('t1')
    await flushPromises()
    expect(log.state).toMatchObject({ status: 'ready', lastSeq: 1 })
  })

  it('drops the log of a deleted task', async () => {
    const { store } = setup(2)
    store.retainEvents('t1')
    await flushPromises()
    store.remove('t1')
    expect(store.eventLog('t1')).toBeUndefined()
    expect(store.get('t1')).toBeUndefined()
  })
})
