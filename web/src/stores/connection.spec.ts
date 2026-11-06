import { flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { createApiClient } from '@/api/client'
import type { BootState } from '@/api/types'
import { FakeEventSource, setupApp } from '@/test/app'
import { iso, makeAccount, makeTask } from '@/test/fixtures'

import { useAccountsStore } from './accounts'
import { useConnectionStore } from './connection'
import { useTasksStore } from './tasks'

function boot(overrides: Partial<BootState> = {}): BootState {
  return {
    health: {
      ok: true,
      version: '9.9',
      commit: 'abc',
      build: 'b1',
      apiVersion: 1,
      pid: 1,
      port: 47831,
      startedAt: iso(),
    },
    settings: {} as BootState['settings'],
    accounts: [makeAccount()],
    backends: [],
    tasks: [makeTask({ id: 't1' })],
    projects: [],
    lastEventId: '41',
    ...overrides,
  }
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

const hello = (build = 'b1', instance = 'i1') =>
  JSON.stringify({ instance, pid: 1, build, version: '9.9', serverTime: iso() })

describe('connection store', () => {
  let reload: ReturnType<typeof vi.fn<() => void>>

  beforeEach(async () => {
    await setupApp()
    vi.useFakeTimers()
    FakeEventSource.instances = []
    reload = vi.fn<() => void>()
  })

  afterEach(() => {
    useConnectionStore().stop()
    vi.useRealTimers()
  })

  function start(fetchImpl: typeof fetch) {
    const store = useConnectionStore()
    store.start({
      api: createApiClient({ fetch: fetchImpl }),
      createSource: (url) => new FakeEventSource(url),
      reload,
      random: () => 0,
    })
    return store
  }

  it('reads /state first, then opens the stream from its event id and goes online', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>(async () => json(boot()))
    const store = start(fetch)
    expect(store.status).toBe('connecting')
    expect(FakeEventSource.instances).toHaveLength(0)
    await flushPromises()
    expect(fetch.mock.calls[0]?.[0]).toBe('/api/v1/state')
    expect(FakeEventSource.last.url).toBe('/api/v1/events?lastEventId=41')
    expect(store.health?.version).toBe('9.9')
    expect(useTasksStore().get('t1')?.title).toBe('Fix the login form')
    expect(useAccountsStore().accounts.map((a) => a.id)).toEqual(['a1'])
    FakeEventSource.last.open()
    expect(store.status).toBe('online')
  })

  it('shows offline with a retry time when /state fails, and retries until it works', async () => {
    const fetch = vi
      .fn<typeof globalThis.fetch>()
      .mockRejectedValueOnce(new TypeError('refused'))
      .mockRejectedValueOnce(new TypeError('refused'))
      .mockResolvedValue(json(boot()))
    const store = start(fetch)
    await flushPromises()
    expect(store.status).toBe('offline')
    expect(store.retryAt).toBe(Date.now() + 1000)
    expect(FakeEventSource.instances).toHaveLength(0)
    await vi.advanceTimersByTimeAsync(1000)
    expect(fetch).toHaveBeenCalledTimes(2)
    store.retryNow()
    await flushPromises()
    expect(fetch).toHaveBeenCalledTimes(3)
    expect(FakeEventSource.instances).toHaveLength(1)
    FakeEventSource.last.open()
    expect(store.status).toBe('online')
    expect(store.retryAt).toBeNull()
  })

  it('reconnects the stream and retries at once when the browser comes back online', async () => {
    const store = start(vi.fn(async () => json(boot())))
    await flushPromises()
    FakeEventSource.last.open()
    FakeEventSource.last.fail()
    expect(store.status).toBe('offline')
    window.dispatchEvent(new Event('online'))
    expect(FakeEventSource.instances).toHaveLength(2)
    FakeEventSource.last.open()
    expect(store.status).toBe('online')
  })

  it('applies task and account frames', async () => {
    start(vi.fn(async () => json(boot())))
    await flushPromises()
    const source = FakeEventSource.last
    source.open()
    source.emit('task.created', JSON.stringify(makeTask({ id: 't2', title: 'New' })), '42')
    source.emit('task.updated', JSON.stringify(makeTask({ id: 't1', status: 'done' })), '43')
    source.emit('task.deleted', JSON.stringify({ id: 't2' }), '44')
    source.emit('account.updated', JSON.stringify(makeAccount({ id: 'a2', priority: 5 })), '45')
    const tasks = useTasksStore()
    expect(tasks.get('t2')).toBeUndefined()
    expect(tasks.get('t1')?.status).toBe('done')
    expect(useAccountsStore().accounts.map((a) => a.id)).toEqual(['a2', 'a1'])
  })

  it('on reset reads /state again and replays only the frames newer than the snapshot', async () => {
    let resolveSecond: (value: Response) => void = () => {}
    const fetch = vi
      .fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(json(boot()))
      .mockReturnValueOnce(new Promise<Response>((resolve) => (resolveSecond = resolve)))
    start(fetch)
    await flushPromises()
    const source = FakeEventSource.last
    source.open()
    source.emit('reset', '{"reason":"too_old"}', '50')
    expect(fetch).toHaveBeenCalledTimes(2)
    // While /state is read: one frame the snapshot already covers, one that is newer.
    source.emit('task.updated', JSON.stringify(makeTask({ id: 't1', title: 'covered' })), '60')
    source.emit('task.updated', JSON.stringify(makeTask({ id: 't1', title: 'newer' })), '62')
    resolveSecond(json(boot({ lastEventId: '61', tasks: [makeTask({ id: 't1', title: 'snap' })] })))
    await flushPromises()
    expect(useTasksStore().get('t1')?.title).toBe('newer')
    expect(FakeEventSource.instances).toHaveLength(1)
  })

  it('reloads when the daemon serves another web bundle', async () => {
    start(vi.fn(async () => json(boot())))
    await flushPromises()
    FakeEventSource.last.open()
    FakeEventSource.last.emit('hello', hello('b1'))
    expect(reload).not.toHaveBeenCalled()
    FakeEventSource.last.emit('hello', hello('b2'))
    expect(reload).toHaveBeenCalledTimes(1)
  })

  it('resyncs when hello comes from a restarted daemon', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>(async () => json(boot()))
    start(fetch)
    await flushPromises()
    FakeEventSource.last.open()
    FakeEventSource.last.emit('hello', hello('b1', 'i1'))
    FakeEventSource.last.emit('hello', hello('b1', 'i1'))
    expect(fetch).toHaveBeenCalledTimes(1)
    FakeEventSource.last.emit('hello', hello('b1', 'i2'))
    await flushPromises()
    expect(fetch).toHaveBeenCalledTimes(2)
  })
})
