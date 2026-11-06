import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { FakeEventSource } from '@/test/app'

import { backoffDelay, createEventStream, MAX_BACKOFF_MS } from './sse'

interface Events {
  hello: { version: string }
  task: { id: string }
}

describe('backoffDelay', () => {
  it('grows by 1.8x from 1 s and never exceeds 10 s', () => {
    const noJitter = () => 0
    expect(backoffDelay(0, noJitter)).toBe(1000)
    expect(backoffDelay(1, noJitter)).toBe(1800)
    expect(backoffDelay(20, noJitter)).toBe(MAX_BACKOFF_MS)
    expect(backoffDelay(20, () => 1)).toBeLessThanOrEqual(MAX_BACKOFF_MS)
  })
})

describe('event stream', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    FakeEventSource.instances = []
  })

  afterEach(() => vi.useRealTimers())

  function setup() {
    const states: [string, number | null][] = []
    const stream = createEventStream<Events>({
      url: '/api/v1/events',
      createSource: (url) => new FakeEventSource(url),
      onState: (state, retry) => states.push([state, retry]),
      random: () => 0,
    })
    return { stream, states }
  }

  it('dispatches named JSON events to typed handlers', () => {
    const { stream } = setup()
    const seen: unknown[] = []
    stream.on('task', (data, meta) => seen.push([data.id, meta.id]))
    stream.start()
    FakeEventSource.last.open()
    FakeEventSource.last.emit('task', '{"id":"t1"}', '7')
    expect(seen).toEqual([['t1', '7']])
    expect(stream.lastEventId).toBe('7')
  })

  it('reconnects with backoff and resumes from the last event id', () => {
    const { stream, states } = setup()
    const seen: string[] = []
    stream.on('hello', (data) => seen.push(data.version))
    stream.start()
    FakeEventSource.last.open()
    FakeEventSource.last.emit('hello', '{"version":"1"}', '42')
    FakeEventSource.last.fail()
    expect(FakeEventSource.last.closed).toBe(true)
    expect(states.at(-1)).toEqual(['closed', 1000])

    vi.advanceTimersByTime(999)
    expect(FakeEventSource.instances).toHaveLength(1)
    vi.advanceTimersByTime(1)
    expect(FakeEventSource.instances).toHaveLength(2)
    expect(FakeEventSource.last.url).toBe('/api/v1/events?lastEventId=42')

    FakeEventSource.last.fail()
    expect(states.at(-1)).toEqual(['closed', 1800])
    stream.retryNow()
    expect(FakeEventSource.instances).toHaveLength(3)
    FakeEventSource.last.open()
    FakeEventSource.last.emit('hello', '{"version":"2"}')
    expect(seen).toEqual(['1', '2'])
    expect(states.at(-1)).toEqual(['open', null])
  })

  it('opens the first connection from a given resume point', () => {
    const stream = createEventStream<Events>({
      url: '/api/v1/events',
      createSource: (url) => new FakeEventSource(url),
      lastEventId: '7',
    })
    stream.setLastEventId('9')
    expect(stream.running).toBe(false)
    stream.start()
    expect(stream.running).toBe(true)
    expect(FakeEventSource.last.url).toBe('/api/v1/events?lastEventId=9')
    stream.stop()
  })

  it('stops for good', () => {
    const { stream } = setup()
    stream.start()
    stream.stop()
    vi.advanceTimersByTime(MAX_BACKOFF_MS * 2)
    expect(FakeEventSource.instances).toHaveLength(1)
    expect(FakeEventSource.last.closed).toBe(true)
    expect(stream.state).toBe('closed')
  })
})
