import { flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { createApiClient } from '@/api/client'
import type { BootState } from '@/api/types'
import { FakeEventSource, setupApp } from '@/test/app'
import { iso, makeAccount, makeHealthResources, makeTask } from '@/test/fixtures'

import { useAccountsStore } from './accounts'
import { HIDDEN_PAUSE_MS, useConnectionStore } from './connection'
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
      homeId: '0123456789abcdef',
      os: 'windows',
      memory: { availPhysMB: 0, availCommitMB: 0, known: false },
      lowMemory: false,
      ...makeHealthResources(),
    },
    settings: {} as BootState['settings'],
    accounts: [makeAccount()],
    backends: [],
    tasks: [makeTask({ id: 't1' })],
    projects: [],
    lastEventId: 'a1b2c3d4.41',
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
    expect(FakeEventSource.last.url).toBe('/api/v1/events?lastEventId=a1b2c3d4.41')
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

  describe('hidden tab', () => {
    let visibility: DocumentVisibilityState = 'visible'

    function setVisibility(next: DocumentVisibilityState) {
      visibility = next
      document.dispatchEvent(new Event('visibilitychange'))
    }

    beforeEach(() => {
      visibility = 'visible'
      vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibility)
    })

    afterEach(() => vi.restoreAllMocks())

    async function online(fetch: typeof globalThis.fetch) {
      const store = start(fetch)
      await flushPromises()
      FakeEventSource.last.open()
      return store
    }

    it('keeps the stream through a short absence', async () => {
      const fetch = vi.fn<typeof globalThis.fetch>(async () => json(boot()))
      const store = await online(fetch)
      setVisibility('hidden')
      await vi.advanceTimersByTimeAsync(HIDDEN_PAUSE_MS - 1000)
      setVisibility('visible')
      await vi.advanceTimersByTimeAsync(HIDDEN_PAUSE_MS)
      expect(FakeEventSource.instances).toHaveLength(1)
      expect(FakeEventSource.last.closed).toBe(false)
      expect(store.paused).toBe(false)
      expect(fetch).toHaveBeenCalledTimes(1)
    })

    it('closes the stream after 5 minutes hidden, quietly, and reads nothing until the tab shows', async () => {
      const fetch = vi.fn<typeof globalThis.fetch>(async () => json(boot()))
      const store = await online(fetch)
      const first = FakeEventSource.last
      first.emit('task.created', JSON.stringify(makeTask({ id: 't2' })), 'a1b2c3d4.50')
      setVisibility('hidden')
      await vi.advanceTimersByTimeAsync(HIDDEN_PAUSE_MS)
      expect(first.closed).toBe(true)
      expect(store.paused).toBe(true)
      // A deliberate pause, not an outage: no offline status, no retry countdown.
      expect(store.status).toBe('online')
      expect(store.retryAt).toBeNull()
      window.dispatchEvent(new Event('online'))
      await vi.advanceTimersByTimeAsync(60 * 60_000)
      expect(FakeEventSource.instances).toHaveLength(1)
      expect(fetch).toHaveBeenCalledTimes(1)

      // Visible again: the stream reopens from the last event, and missed frames are replayed.
      setVisibility('visible')
      expect(store.paused).toBe(false)
      expect(FakeEventSource.instances).toHaveLength(2)
      expect(FakeEventSource.last.url).toBe('/api/v1/events?lastEventId=a1b2c3d4.50')
      FakeEventSource.last.open()
      FakeEventSource.last.emit(
        'task.updated',
        JSON.stringify(makeTask({ id: 't1', title: 'while hidden' })),
        'a1b2c3d4.51',
      )
      expect(useTasksStore().get('t1')?.title).toBe('while hidden')
      expect(store.status).toBe('online')
    })

    it('resyncs from /state when the daemon cannot replay what the tab missed', async () => {
      const fetch = vi
        .fn<typeof globalThis.fetch>()
        .mockResolvedValueOnce(json(boot()))
        .mockResolvedValueOnce(
          json(boot({ tasks: [makeTask({ id: 't1', title: 'fresh' })], lastEventId: 'b.9' })),
        )
      const store = await online(fetch)
      setVisibility('hidden')
      await vi.advanceTimersByTimeAsync(HIDDEN_PAUSE_MS)
      setVisibility('visible')
      FakeEventSource.last.open()
      FakeEventSource.last.emit('reset', '{"reason":"too_old"}')
      await flushPromises()
      expect(fetch).toHaveBeenCalledTimes(2)
      expect(useTasksStore().get('t1')?.title).toBe('fresh')
      expect(store.status).toBe('online')
    })

    it('stops retrying an unreachable daemon while hidden, and tries again once visible', async () => {
      const fetch = vi.fn<typeof globalThis.fetch>().mockRejectedValue(new TypeError('refused'))
      const store = start(fetch)
      await flushPromises()
      expect(store.status).toBe('offline')
      setVisibility('hidden')
      await vi.advanceTimersByTimeAsync(HIDDEN_PAUSE_MS)
      const calls = fetch.mock.calls.length
      await vi.advanceTimersByTimeAsync(60 * 60_000)
      expect(fetch).toHaveBeenCalledTimes(calls)
      fetch.mockResolvedValue(json(boot()))
      setVisibility('visible')
      await flushPromises()
      expect(fetch).toHaveBeenCalledTimes(calls + 1)
      expect(FakeEventSource.instances).toHaveLength(1)
    })
  })

  it('applies task and account frames', async () => {
    start(vi.fn(async () => json(boot())))
    await flushPromises()
    const source = FakeEventSource.last
    source.open()
    source.emit('task.created', JSON.stringify(makeTask({ id: 't2', title: 'New' })), 'a1b2c3d4.42')
    source.emit(
      'task.updated',
      JSON.stringify(makeTask({ id: 't1', status: 'done' })),
      'a1b2c3d4.43',
    )
    source.emit('task.deleted', JSON.stringify({ id: 't2' }), 'a1b2c3d4.44')
    source.emit(
      'account.updated',
      JSON.stringify(makeAccount({ id: 'a2', priority: 5 })),
      'a1b2c3d4.45',
    )
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
    source.emit('reset', '{"reason":"too_old"}', 'a1b2c3d4.50')
    expect(fetch).toHaveBeenCalledTimes(2)
    // While /state is read: one frame the snapshot already covers, one that is newer.
    source.emit(
      'task.updated',
      JSON.stringify(makeTask({ id: 't1', title: 'covered' })),
      'a1b2c3d4.60',
    )
    source.emit(
      'task.updated',
      JSON.stringify(makeTask({ id: 't1', title: 'newer' })),
      'a1b2c3d4.62',
    )
    resolveSecond(
      json(boot({ lastEventId: 'a1b2c3d4.60', tasks: [makeTask({ id: 't1', title: 'snap' })] })),
    )
    await flushPromises()
    expect(useTasksStore().get('t1')?.title).toBe('newer')
    expect(FakeEventSource.instances).toHaveLength(1)
  })

  it('routes sign-in frames to the accounts store and other frames to subscribers', async () => {
    const store = start(vi.fn(async () => json(boot())))
    await flushPromises()
    const seen: string[] = []
    const off = store.subscribe('roles.changed', (data) => seen.push(data.changedAt))
    const source = FakeEventSource.last
    source.open()
    source.emit('roles.changed', JSON.stringify({ changedAt: 'x1' }), 'a1b2c3d4.42')
    source.emit(
      'account.login',
      JSON.stringify({ accountId: 'a1', state: 'waiting_code', userCode: 'AB-12' }),
      'a1b2c3d4.43',
    )
    off()
    source.emit('roles.changed', JSON.stringify({ changedAt: 'x2' }), 'a1b2c3d4.44')
    expect(seen).toEqual(['x1'])
    expect(useAccountsStore().logins.a1?.state).toBe('waiting_code')
  })

  it('runs resync listeners after every snapshot but the first (old bug B9)', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>(async () => json(boot()))
    const store = start(fetch)
    const resyncs = vi.fn()
    store.onResync(resyncs)
    await flushPromises()
    expect(resyncs).not.toHaveBeenCalled()
    const source = FakeEventSource.last
    source.open()
    source.emit('reset', '{"reason":"too_old"}', 'a1b2c3d4.50')
    await flushPromises()
    expect(resyncs).toHaveBeenCalledTimes(1)
  })

  it('replays held frames by "newer wins" when the snapshot id was not among them', async () => {
    let resolveSecond: (value: Response) => void = () => {}
    const fetch = vi
      .fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(json(boot()))
      .mockReturnValueOnce(new Promise<Response>((resolve) => (resolveSecond = resolve)))
    start(fetch)
    await flushPromises()
    const source = FakeEventSource.last
    source.open()
    source.emit('reset', '{"reason":"restart"}')
    const stale = makeTask({ id: 't1', title: 'stale', updatedAt: iso(1000) })
    const fresh = makeTask({ id: 't2', title: 'fresh', updatedAt: iso(9000) })
    source.emit('task.updated', JSON.stringify(stale), 'ffff0000.3')
    source.emit('task.created', JSON.stringify(fresh), 'ffff0000.4')
    const snap = makeTask({ id: 't1', title: 'snap', updatedAt: iso(5000) })
    resolveSecond(json(boot({ lastEventId: 'ffff0000.2', tasks: [snap] })))
    await flushPromises()
    const tasks = useTasksStore()
    expect(tasks.get('t1')?.title).toBe('snap')
    expect(tasks.get('t2')?.title).toBe('fresh')
  })

  it('runs one more /state pass when a reset arrives while /state is pending', async () => {
    const pending: ((value: Response) => void)[] = []
    const fetch = vi
      .fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(json(boot()))
      .mockImplementation(() => new Promise<Response>((resolve) => pending.push(resolve)))
    start(fetch)
    await flushPromises()
    const source = FakeEventSource.last
    source.open()
    source.emit('reset', '{"reason":"too_old"}')
    expect(fetch).toHaveBeenCalledTimes(2)
    source.emit('reset', '{"reason":"too_old"}')
    source.emit('reset', '{"reason":"too_old"}')
    expect(fetch).toHaveBeenCalledTimes(2)
    // A frame after the second reset; the second snapshot's id comes before it.
    source.emit(
      'task.updated',
      JSON.stringify(makeTask({ id: 't1', title: 'after' })),
      'a1b2c3d4.71',
    )
    pending[0]?.(json(boot({ tasks: [makeTask({ id: 't1', title: 'first' })] })))
    await flushPromises()
    expect(fetch).toHaveBeenCalledTimes(3)
    pending[1]?.(
      json(boot({ lastEventId: 'a1b2c3d4.70', tasks: [makeTask({ id: 't1', title: 'second' })] })),
    )
    await flushPromises()
    expect(fetch).toHaveBeenCalledTimes(3)
    expect(useTasksStore().get('t1')?.title).toBe('after')
  })

  it('reads /health again after settings.updated, and only the newest answer applies', async () => {
    const answers: ((value: Response) => void)[] = []
    const fetch = vi.fn<typeof globalThis.fetch>(async (input) => {
      if (String(input) === '/api/v1/state') return json(boot())
      return new Promise<Response>((resolve) => answers.push(resolve))
    })
    const store = start(fetch)
    await flushPromises()
    const source = FakeEventSource.last
    source.open()
    const withGates = (value: number) => ({
      ...boot().health,
      resources: {
        ...makeHealthResources().resources,
        gateConcurrency: { value, source: 'explicit' as const },
      },
    })
    source.emit('settings.updated', JSON.stringify({ gateConcurrency: 3 }), 'a1b2c3d4.42')
    source.emit('settings.updated', JSON.stringify({ gateConcurrency: 4 }), 'a1b2c3d4.43')
    await flushPromises()
    const reads = fetch.mock.calls.filter(([input]) => String(input) === '/api/v1/health')
    expect(reads).toHaveLength(2)
    answers[1]?.(json(withGates(4)))
    await flushPromises()
    expect(store.health?.resources.gateConcurrency.value).toBe(4)
    // The first read answers last: it is older than what is shown and does not apply.
    answers[0]?.(json(withGates(3)))
    await flushPromises()
    expect(store.health?.resources.gateConcurrency.value).toBe(4)
    expect(store.settings?.gateConcurrency).toBe(4)
  })

  it('keeps a resources.changed frame that comes while a /health read is on its way', async () => {
    const answers: ((value: Response) => void)[] = []
    const low = { availPhysMB: 300, availCommitMB: 900, known: true }
    const fetch = vi.fn<typeof globalThis.fetch>(async (input) => {
      if (String(input) === '/api/v1/state') {
        return json(boot({ health: { ...boot().health, lowMemory: true, memory: low } }))
      }
      return new Promise<Response>((resolve) => answers.push(resolve))
    })
    const store = start(fetch)
    await flushPromises()
    const source = FakeEventSource.last
    source.open()
    // A save while memory is low: the read starts, the daemon answers while lowMemory is still true...
    source.emit('settings.updated', JSON.stringify({ gateConcurrency: 3 }), 'a1b2c3d4.42')
    await flushPromises()
    // ...then the watchdog recovers, and its frame comes before that answer.
    const recovered = { availPhysMB: 9000, availCommitMB: 12000, known: true }
    source.emit(
      'resources.changed',
      JSON.stringify({ lowMemory: false, memory: recovered }),
      'a1b2c3d4.43',
    )
    expect(store.health?.lowMemory).toBe(false)
    const { resources } = makeHealthResources()
    answers[0]?.(
      json({
        ...boot().health,
        lowMemory: true,
        memory: low,
        resources: { ...resources, gateConcurrency: { value: 3, source: 'explicit' } },
      }),
    )
    await flushPromises()
    // The answer's resources apply; its older lowMemory and memory do not.
    expect(store.health?.resources.gateConcurrency).toEqual({ value: 3, source: 'explicit' })
    expect(store.health?.lowMemory).toBe(false)
    expect(store.health?.memory).toEqual(recovered)

    // A read that starts after the frame applies whole.
    source.emit('settings.updated', JSON.stringify({ gateConcurrency: 2 }), 'a1b2c3d4.44')
    await flushPromises()
    answers[1]?.(json({ ...boot().health, lowMemory: true, memory: low }))
    await flushPromises()
    expect(store.health?.lowMemory).toBe(true)
  })

  it('careful replay keeps the snapshot for settings, older accounts and deleted tasks', async () => {
    let resolveSecond: (value: Response) => void = () => {}
    const fetch = vi
      .fn<typeof globalThis.fetch>()
      .mockResolvedValueOnce(json(boot()))
      .mockReturnValueOnce(new Promise<Response>((resolve) => (resolveSecond = resolve)))
    const store = start(fetch)
    await flushPromises()
    const source = FakeEventSource.last
    source.open()
    source.emit('reset', '{"reason":"restart"}')
    source.emit('settings.updated', JSON.stringify({ port: 1 }), 'ffff0000.3')
    source.emit(
      'account.updated',
      JSON.stringify(makeAccount({ id: 'a1', label: 'old', updatedAt: iso(1000) })),
      'ffff0000.4',
    )
    source.emit(
      'account.updated',
      JSON.stringify(makeAccount({ id: 'a2', label: 'new', updatedAt: iso(1000) })),
      'ffff0000.5',
    )
    source.emit('task.updated', JSON.stringify(makeTask({ id: 'gone' })), 'ffff0000.6')
    source.emit('task.deleted', JSON.stringify({ id: 'gone' }), 'ffff0000.7')
    source.emit('task.updated', JSON.stringify(makeTask({ id: 'gone' })), 'ffff0000.8')
    resolveSecond(
      json(
        boot({
          lastEventId: 'ffff0000.1',
          settings: { port: 2 } as BootState['settings'],
          accounts: [makeAccount({ id: 'a1', label: 'snap', updatedAt: iso(5000) })],
        }),
      ),
    )
    await flushPromises()
    expect(store.settings?.port).toBe(2)
    const accounts = useAccountsStore()
    expect(accounts.get('a1')?.label).toBe('snap')
    expect(accounts.get('a2')?.label).toBe('new')
    expect(useTasksStore().get('gone')).toBeUndefined()
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
