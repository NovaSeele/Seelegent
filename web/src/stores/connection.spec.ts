import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { createApiClient } from '@/api/client'
import { FakeEventSource, setupApp } from '@/test/app'

import { useConnectionStore } from './connection'

describe('connection store', () => {
  beforeEach(async () => {
    await setupApp()
    vi.useFakeTimers()
    FakeEventSource.instances = []
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
    })
    return store
  }

  it('goes online when the stream opens and reads /health', async () => {
    const fetch = vi.fn<typeof globalThis.fetch>(
      async () =>
        new Response('{"status":"ok","version":"9.9"}', {
          headers: { 'Content-Type': 'application/json' },
        }),
    )
    const store = start(fetch)
    expect(store.status).toBe('connecting')
    FakeEventSource.last.open()
    expect(store.status).toBe('online')
    await vi.runAllTimersAsync()
    expect(fetch.mock.calls[0]?.[0]).toBe('/api/v1/health')
    expect(store.health).toEqual({ status: 'ok', version: '9.9' })
  })

  it('is offline with a retry time after the stream fails, and retries on demand', () => {
    const store = start(vi.fn())
    FakeEventSource.last.fail()
    expect(store.status).toBe('offline')
    expect(store.retryAt).toBeGreaterThan(Date.now())
    store.retryNow()
    expect(FakeEventSource.instances).toHaveLength(2)
    expect(store.status).toBe('offline') // stays offline until the stream opens
    FakeEventSource.last.open()
    expect(store.status).toBe('online')
    expect(store.retryAt).toBeNull()
  })

  it('retries at once when the browser comes back online', () => {
    start(vi.fn())
    FakeEventSource.last.fail()
    window.dispatchEvent(new Event('online'))
    expect(FakeEventSource.instances).toHaveLength(2)
  })
})
