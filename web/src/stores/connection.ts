/**
 * Connection of the dashboard to the daemon: owns the single event stream and tells the shell whether to show
 * the "connection lost" banner. On every (re)connect it reads /api/v1/health for the daemon's version.
 */
import { defineStore } from 'pinia'
import { ref, shallowRef } from 'vue'

import {
  api as defaultApi,
  EVENTS_URL,
  type ApiClient,
  type Health,
  type ServerEvents,
} from '@/api'
import { createEventStream, type EventSourceLike, type EventStream } from '@/api/sse'

export type ConnectionStatus = 'connecting' | 'online' | 'offline'

export interface ConnectionDeps {
  api?: ApiClient
  createSource?: (url: string) => EventSourceLike
}

export const useConnectionStore = defineStore('connection', () => {
  const status = ref<ConnectionStatus>('connecting')
  /** When the next reconnect attempt runs (epoch ms), while offline. */
  const retryAt = ref<number | null>(null)
  const health = ref<Health | null>(null)
  /** True once the daemon has answered at least once in this page. */
  const hasConnected = ref(false)
  const stream = shallowRef<EventStream<ServerEvents> | null>(null)
  let client: ApiClient = defaultApi
  let cleanup: (() => void) | null = null

  async function checkHealth(): Promise<void> {
    try {
      health.value = await client.get<Health>('/health')
    } catch {
      // The event stream decides online/offline; a failed health read only leaves the version unknown.
    }
  }

  function setStatus(next: ConnectionStatus): void {
    status.value = next
  }

  /** Opens the event stream (once). Dependencies can be swapped in tests. */
  function start(deps: ConnectionDeps = {}): EventStream<ServerEvents> {
    if (stream.value) return stream.value
    client = deps.api ?? defaultApi
    const events = createEventStream<ServerEvents>({
      url: EVENTS_URL,
      createSource: deps.createSource,
      onState(state, retryIn) {
        if (state === 'open') {
          status.value = 'online'
          retryAt.value = null
          hasConnected.value = true
          void checkHealth()
        } else if (state === 'closed') {
          status.value = 'offline'
          retryAt.value = retryIn === null ? null : Date.now() + retryIn
        } else if (status.value !== 'offline') {
          status.value = 'connecting'
        }
      },
    })
    stream.value = events
    events.start()

    if (typeof window !== 'undefined') {
      const retry = () => events.retryNow()
      const onVisible = () => {
        if (document.visibilityState === 'visible') retry()
      }
      window.addEventListener('online', retry)
      document.addEventListener('visibilitychange', onVisible)
      cleanup = () => {
        window.removeEventListener('online', retry)
        document.removeEventListener('visibilitychange', onVisible)
      }
    }
    return events
  }

  function retryNow(): void {
    stream.value?.retryNow()
  }

  function stop(): void {
    stream.value?.stop()
    stream.value = null
    cleanup?.()
    cleanup = null
  }

  return {
    status,
    retryAt,
    health,
    hasConnected,
    stream,
    setStatus,
    start,
    retryNow,
    stop,
    checkHealth,
  }
})
