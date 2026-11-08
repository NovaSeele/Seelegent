/**
 * Connection of the dashboard to the daemon and the sync of its data.
 *
 * Boot: GET /state (everything the first paint needs, with the SSE id of that moment), then the event stream
 * opened from that id, so nothing between the snapshot and the stream is lost. A failed /state read retries with
 * the stream's backoff and shows the offline banner (it never leaves an empty dashboard behind, old bug B3).
 *
 * While streaming:
 * - `reset` (the daemon cannot resume this stream): /state is read again; frames that arrive meanwhile are held
 *   and the ones newer than the snapshot are applied after it. A reset during that read runs one more pass.
 * - `hello` with another `build` than the page was loaded with: the dashboard reloads to the new bundle.
 *   Another `instance` (daemon restart) also resyncs, in case the daemon's own reset was missed.
 * - task.* frames go to the tasks store, account.* (sign-in progress included) to the accounts store,
 *   settings.updated and resources.changed (the memory watchdog's lowMemory, into health) here. Other frames
 *   (providers.changed, roles.changed, memory.changed, hook.ran) go to the stores that `subscribe` to them,
 *   through the same hold-and-replay path, so none is lost during a resync.
 * - After every snapshot but the first, `onResync` listeners run: data that is not in /state (providers, roles,
 *   usage) is read again, since frames about it may have fallen between the old stream and the new one (old
 *   bug B9: pages stayed stale after a reconnect).
 */
import { defineStore } from 'pinia'
import { ref, shallowRef } from 'vue'

import { api as defaultApi, EVENTS_URL, type ApiClient } from '@/api'
import { SERVER_EVENT_NAMES, type HelloFrame, type ServerEvents } from '@/api/events'
import {
  backoffDelay,
  createEventStream,
  type EventMeta,
  type EventSourceLike,
  type EventStream,
} from '@/api/sse'
import { createTasksApi, type TasksApi } from '@/api/tasks'
import type { Backend, BootState, Health, Project, Settings } from '@/api/types'

import { useAccountsStore } from './accounts'
import { useTasksStore } from './tasks'

export type ConnectionStatus = 'connecting' | 'online' | 'offline'

export interface ConnectionDeps {
  api?: ApiClient
  createSource?: (url: string) => EventSourceLike
  /** Called when the daemon serves another web bundle (default: location.reload). */
  reload?: () => void
  random?: () => number
}

/** Frames held while a resync reads /state; beyond this the oldest are dropped (the snapshot covers them). */
export const MAX_HELD_FRAMES = 5000

/** Frames other stores subscribe to (everything the connection does not apply itself). */
export type NoticeName = Exclude<
  keyof ServerEvents,
  | 'hello'
  | 'reset'
  | 'task.created'
  | 'task.updated'
  | 'task.deleted'
  | 'task.event'
  | 'account.updated'
  | 'account.deleted'
  | 'account.login'
  | 'settings.updated'
  | 'resources.changed'
>

interface HeldFrame {
  name: keyof ServerEvents
  data: unknown
  id: string
}

export const useConnectionStore = defineStore('connection', () => {
  const status = ref<ConnectionStatus>('connecting')
  /** When the next reconnect attempt runs (epoch ms), while offline. */
  const retryAt = ref<number | null>(null)
  const health = shallowRef<Health | null>(null)
  const settings = shallowRef<Settings | null>(null)
  const backends = shallowRef<readonly Backend[]>([])
  const projects = shallowRef<readonly Project[]>([])
  /** True once the daemon has answered at least once in this page. */
  const hasConnected = ref(false)
  /** Server clock minus local clock (ms), from hello.serverTime; elapsed times use it. */
  const clockOffsetMs = ref(0)
  const stream = shallowRef<EventStream<ServerEvents> | null>(null)

  let client: ApiClient = defaultApi
  let tasksApi: TasksApi = createTasksApi(defaultApi)
  let reload: () => void = () => window.location.reload()
  let random: () => number = Math.random
  let cleanup: (() => void) | null = null
  /** The web bundle this page runs (from the first snapshot). */
  let pageBuild = ''
  let instance = ''
  let syncing = false
  /** A resync was asked for while one was reading /state: run one more pass when it ends. */
  let again = false
  let held: HeldFrame[] = []
  let snapshots = 0
  const listeners = new Map<string, Set<(data: unknown) => void>>()
  const resyncListeners = new Set<() => void>()
  let attempt = 0
  let retryTimer: ReturnType<typeof setTimeout> | undefined
  let stopped = false

  function setStatus(next: ConnectionStatus): void {
    status.value = next
  }

  function checkBuild(build: string | undefined): boolean {
    if (!build) return false
    if (!pageBuild) {
      pageBuild = build
      return false
    }
    if (build === pageBuild) return false
    reload()
    return true
  }

  function applySnapshot(state: BootState): void {
    if (checkBuild(state.health.build)) return
    health.value = state.health
    settings.value = state.settings
    backends.value = state.backends
    projects.value = state.projects
    useAccountsStore().replaceAll(state.accounts)
    useTasksStore().applySnapshot(state.tasks)
    if (snapshots++ > 0) for (const listener of [...resyncListeners]) listener()
  }

  /**
   * Reads /state and applies it; frames received meanwhile are held and the newer ones replayed after. Called
   * while a read is pending, it asks for one more pass instead: that read may predate what the new reset is about.
   */
  async function sync(keepHeld = false): Promise<void> {
    if (stopped) return
    if (syncing) {
      again = true
      return
    }
    syncing = true
    again = false
    if (!keepHeld) held = []
    clearTimeout(retryTimer)
    retryTimer = undefined
    let state: BootState
    try {
      state = await tasksApi.bootState()
    } catch {
      syncing = false
      again = false
      if (stopped) return
      const events = stream.value
      // Before the stream runs, a failed boot read is the connection state; once it runs the stream says.
      const delay = backoffDelay(attempt++, random)
      if (!events?.running) {
        status.value = 'offline'
        retryAt.value = Date.now() + delay
      }
      retryTimer = setTimeout(() => void sync(), delay)
      return
    }
    if (stopped) return
    attempt = 0
    applySnapshot(state)
    if (again) {
      // Keep the held frames: the next snapshot's id is looked up among them.
      syncing = false
      await sync(true)
      return
    }
    // Ids are opaque: frames up to the snapshot's own id are covered by it. When that id was not among the held
    // frames, every held frame is replayed with "newer wins" rules (summaries by updatedAt, events by seq).
    const at = held.findIndex((frame) => frame.id === state.lastEventId)
    const replay = at >= 0 ? held.slice(at + 1) : held
    const careful = at < 0
    held = []
    syncing = false
    const deleted = careful ? deletedIn(replay) : null
    for (const frame of replay) apply(frame.name, frame.data, careful, deleted)
    const events = stream.value
    if (events && !events.running) {
      events.setLastEventId(state.lastEventId)
      events.start()
    }
  }

  function onHello(hello: HelloFrame): void {
    hasConnected.value = true
    const serverTime = Date.parse(hello.serverTime)
    if (Number.isFinite(serverTime)) clockOffsetMs.value = serverTime - Date.now()
    if (checkBuild(hello.build)) return
    const restarted = instance !== '' && hello.instance !== instance
    instance = hello.instance
    if (restarted) void sync()
  }

  /** Task and account ids that a replay deletes, keyed `task:<id>` / `account:<id>`. */
  function deletedIn(frames: readonly HeldFrame[]): Set<string> {
    const ids = new Set<string>()
    for (const frame of frames) {
      if (frame.name === 'task.deleted') {
        ids.add(`task:${(frame.data as ServerEvents['task.deleted']).id}`)
      } else if (frame.name === 'account.deleted') {
        ids.add(`account:${(frame.data as ServerEvents['account.deleted']).id}`)
      }
    }
    return ids
  }

  /**
   * `careful`: the frame may be older than the snapshot just applied (a replay after a resync whose snapshot id
   * was not among the held frames). Then summaries and accounts apply only when newer, settings are left to the
   * snapshot (they carry no time), and nothing a later frame of the replay deletes (`deleted`) comes back.
   */
  function apply(
    name: keyof ServerEvents,
    data: unknown,
    careful = false,
    deleted: ReadonlySet<string> | null = null,
  ): void {
    const tasks = useTasksStore()
    const accounts = useAccountsStore()
    switch (name) {
      case 'task.created':
      case 'task.updated': {
        const task = data as ServerEvents['task.updated']
        if (!careful) tasks.upsert(task)
        else if (!deleted?.has(`task:${task.id}`)) tasks.upsertIfNewer(task)
        break
      }
      case 'task.deleted':
        tasks.remove((data as ServerEvents['task.deleted']).id)
        break
      case 'task.event':
        tasks.applyEvent(data as ServerEvents['task.event'])
        break
      case 'account.updated': {
        const account = data as ServerEvents['account.updated']
        if (!careful) accounts.upsert(account)
        else if (!deleted?.has(`account:${account.id}`)) accounts.upsertIfNewer(account)
        break
      }
      case 'account.deleted':
        accounts.remove((data as ServerEvents['account.deleted']).id)
        break
      case 'account.login':
        accounts.applyLogin(data as ServerEvents['account.login'])
        break
      case 'settings.updated':
        // A careful replay skips settings on purpose: a settings frame carries no time, so it cannot be ordered
        // against the snapshot just applied, and an older frame would undo a newer value. The snapshot wins; a
        // change that raced it shows with the next settings.updated or resync. Live frames always apply.
        if (!careful) settings.value = data as Settings
        break
      case 'resources.changed': {
        // Like settings, it carries no time: a careful replay leaves lowMemory to the snapshot just applied.
        const change = data as ServerEvents['resources.changed']
        if (!careful && health.value) {
          health.value = { ...health.value, lowMemory: change.lowMemory, memory: change.memory }
        }
        break
      }
      default:
        for (const listener of [...(listeners.get(name) ?? [])]) listener(data)
        break
    }
  }

  /** Calls `handler` with every frame of `name`; returns the unsubscribe function. */
  function subscribe<K extends NoticeName>(
    name: K,
    handler: (data: ServerEvents[K]) => void,
  ): () => void {
    const set = listeners.get(name) ?? new Set<(data: unknown) => void>()
    const fn = handler as (data: unknown) => void
    set.add(fn)
    listeners.set(name, set)
    return () => set.delete(fn)
  }

  /** Calls `handler` after each resync (every /state snapshot after the first); returns the unsubscribe. */
  function onResync(handler: () => void): () => void {
    resyncListeners.add(handler)
    return () => resyncListeners.delete(handler)
  }

  function onFrame(name: keyof ServerEvents, data: unknown, meta: EventMeta): void {
    if (name === 'hello') return onHello(data as HelloFrame)
    if (name === 'reset') {
      void sync()
      return
    }
    if (syncing) {
      held.push({ name, data, id: meta.id })
      if (held.length > MAX_HELD_FRAMES) held.splice(0, held.length - MAX_HELD_FRAMES)
      return
    }
    apply(name, data)
  }

  /** Boots the connection (once): /state, then the event stream. Dependencies can be swapped in tests. */
  function start(deps: ConnectionDeps = {}): EventStream<ServerEvents> {
    if (stream.value) return stream.value
    stopped = false
    client = deps.api ?? defaultApi
    tasksApi = createTasksApi(client)
    if (deps.reload) reload = deps.reload
    if (deps.random) random = deps.random
    const events = createEventStream<ServerEvents>({
      url: EVENTS_URL,
      createSource: deps.createSource,
      random,
      onState(state, retryIn) {
        if (state === 'open') {
          status.value = 'online'
          retryAt.value = null
          hasConnected.value = true
        } else if (state === 'closed') {
          if (stopped) return
          status.value = 'offline'
          retryAt.value = retryIn === null ? null : Date.now() + retryIn
        } else if (status.value !== 'offline') {
          status.value = 'connecting'
        }
      },
    })
    for (const name of SERVER_EVENT_NAMES) {
      events.on(name, (data, meta) => onFrame(name, data, meta))
    }
    stream.value = events
    void sync()

    if (typeof window !== 'undefined') {
      const retry = () => retryNow()
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

  /** Retries at once: the boot read when it is still failing, else the stream. */
  function retryNow(): void {
    const events = stream.value
    if (!events) return
    if (!events.running) {
      if (!syncing) void sync()
      return
    }
    events.retryNow()
  }

  function stop(): void {
    stopped = true
    clearTimeout(retryTimer)
    retryTimer = undefined
    stream.value?.stop()
    stream.value = null
    cleanup?.()
    cleanup = null
    syncing = false
    again = false
    held = []
    attempt = 0
    pageBuild = ''
    instance = ''
    snapshots = 0
  }

  /**
   * Reads GET /health again (the free memory shown next to the resource settings is read when the daemon answers).
   * A failure keeps the last reading: the connection state says when the daemon is gone.
   */
  async function refreshHealth(signal?: AbortSignal): Promise<void> {
    try {
      const next = await client.get<Health>('/health', { signal })
      if (health.value) health.value = next
    } catch {
      // Kept as it was.
    }
  }

  /** The server's idea of now (epoch ms). */
  function serverNow(): number {
    return Date.now() + clockOffsetMs.value
  }

  return {
    status,
    retryAt,
    health,
    settings,
    backends,
    projects,
    hasConnected,
    clockOffsetMs,
    stream,
    setStatus,
    start,
    retryNow,
    stop,
    sync,
    refreshHealth,
    serverNow,
    subscribe,
    onResync,
  }
})
