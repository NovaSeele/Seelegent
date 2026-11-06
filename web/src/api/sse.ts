/**
 * Reconnecting Server-Sent Events client for /api/v1/events.
 *
 * - One EventSource at a time. On error it is closed and reopened after a backoff of 1 s x 1.8^n, capped at
 *   10 s (with a little jitter); a successful open resets the backoff. `retryNow()` skips the wait (used when the
 *   browser comes back online or the tab becomes visible).
 * - Resume: the id of the last event received is sent back on reconnect. A new EventSource cannot set the
 *   Last-Event-ID header itself, so it goes in the `lastEventId` query parameter (the daemon accepts both).
 * - Events are named; payloads are JSON and dispatched to typed handlers. A payload that is not JSON is passed
 *   as the raw string.
 */
export const MAX_BACKOFF_MS = 10_000
const BASE_BACKOFF_MS = 1_000
const BACKOFF_FACTOR = 1.8

export type StreamState = 'connecting' | 'open' | 'closed'

/** The part of EventSource this client uses (lets tests pass a fake). */
export interface EventSourceLike {
  readonly readyState: number
  onopen: ((this: EventSourceLike, ev: Event) => unknown) | null
  onerror: ((this: EventSourceLike, ev: Event) => unknown) | null
  addEventListener(type: string, listener: (event: MessageEvent<string>) => void): void
  close(): void
}

export interface EventMeta {
  id: string
  type: string
}

export interface EventStreamOptions {
  url: string
  createSource?: (url: string) => EventSourceLike
  /** Called with every state change and the delay before the next attempt (when closed). */
  onState?: (state: StreamState, retryInMs: number | null) => void
  random?: () => number
}

export function backoffDelay(attempt: number, random: () => number = Math.random): number {
  const base = Math.min(MAX_BACKOFF_MS, BASE_BACKOFF_MS * BACKOFF_FACTOR ** attempt)
  const jitter = base * 0.2 * random()
  return Math.min(MAX_BACKOFF_MS, Math.round(base - jitter))
}

export function createEventStream<Events extends object>(options: EventStreamOptions) {
  type Handler<K extends keyof Events> = (data: Events[K], meta: EventMeta) => void
  type AnyHandler = (data: unknown, meta: EventMeta) => void

  const createSource =
    options.createSource ?? ((url: string) => new EventSource(url) as unknown as EventSourceLike)
  const random = options.random ?? Math.random
  const handlers = new Map<string, Set<AnyHandler>>()
  const bound = new Set<string>()
  let source: EventSourceLike | null = null
  let timer: ReturnType<typeof setTimeout> | undefined
  let attempt = 0
  let lastEventId = ''
  let running = false
  let state: StreamState = 'closed'

  function setState(next: StreamState, retryIn: number | null = null) {
    state = next
    options.onState?.(next, retryIn)
  }

  function dispatch(event: MessageEvent<string>) {
    if (event.lastEventId) lastEventId = event.lastEventId
    let data: unknown = event.data
    try {
      data = JSON.parse(event.data)
    } catch {
      // Keep the raw string.
    }
    const meta = { id: event.lastEventId, type: event.type }
    for (const handler of handlers.get(event.type) ?? []) handler(data, meta)
  }

  function bind(type: string) {
    if (!source || bound.has(type)) return
    source.addEventListener(type, dispatch)
    bound.add(type)
  }

  function urlWithResume(): string {
    if (!lastEventId) return options.url
    const sep = options.url.includes('?') ? '&' : '?'
    return `${options.url}${sep}lastEventId=${encodeURIComponent(lastEventId)}`
  }

  function connect() {
    clearTimeout(timer)
    timer = undefined
    source?.close()
    bound.clear()
    setState('connecting')
    const current = createSource(urlWithResume())
    source = current
    for (const type of handlers.keys()) bind(type)
    current.onopen = () => {
      if (source !== current) return
      attempt = 0
      setState('open')
    }
    current.onerror = () => {
      if (source !== current) return
      current.close()
      source = null
      bound.clear()
      if (!running) return
      const delay = backoffDelay(attempt++, random)
      setState('closed', delay)
      timer = setTimeout(connect, delay)
    }
  }

  return {
    get state() {
      return state
    },
    get lastEventId() {
      return lastEventId
    },
    on<K extends keyof Events & string>(type: K, handler: Handler<K>): () => void {
      const set = handlers.get(type) ?? new Set<AnyHandler>()
      set.add(handler as AnyHandler)
      handlers.set(type, set)
      bind(type)
      return () => set.delete(handler as AnyHandler)
    },
    start() {
      if (running) return
      running = true
      connect()
    },
    /** Reconnects now instead of waiting for the backoff timer. */
    retryNow() {
      if (running && state !== 'open') connect()
    },
    stop() {
      running = false
      clearTimeout(timer)
      timer = undefined
      source?.close()
      source = null
      bound.clear()
      setState('closed')
    },
  }
}

export type EventStream<Events extends object> = ReturnType<typeof createEventStream<Events>>
