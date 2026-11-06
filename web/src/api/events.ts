/**
 * Shapes the contract describes without naming them: the SSE frames of /api/v1/events (§7) and the `data` of
 * each task event type (§3.2). TaskEvent.data is `Record<string, unknown>` on the wire, so readers narrow it with
 * the guards below instead of trusting a cast.
 */
import type {
  Account,
  Attention,
  LoginSession,
  Settings,
  TaskEvent,
  TaskSummary,
  Tier,
  Usage,
} from './types'

/** `hello`: first frame of every connection. `instance` changes when the daemon restarts. */
export interface HelloFrame {
  instance: string
  pid: number
  build: string
  version: string
  serverTime: string
}

/** `reset`: the stream cannot resume; refetch /state and keep listening. */
export interface ResetFrame {
  reason: 'too_old' | 'restart' | 'overflow'
}

/** Named SSE events and their payloads. */
export interface ServerEvents {
  hello: HelloFrame
  reset: ResetFrame
  'task.created': TaskSummary
  'task.updated': TaskSummary
  'task.deleted': { id: string }
  'task.event': TaskEvent
  'account.updated': Account
  'account.deleted': { id: string }
  'account.login': LoginSession
  'settings.updated': Settings
  'providers.changed': { changedAt: string }
  'roles.changed': { changedAt: string }
  'members.changed': { changedAt: string }
  'memory.changed': { cwd: string }
  'hook.ran': Record<string, unknown>
}

export type ServerEventName = keyof ServerEvents

export const SERVER_EVENT_NAMES: readonly ServerEventName[] = [
  'hello',
  'reset',
  'task.created',
  'task.updated',
  'task.deleted',
  'task.event',
  'account.updated',
  'account.deleted',
  'account.login',
  'settings.updated',
  'providers.changed',
  'roles.changed',
  'members.changed',
  'memory.changed',
  'hook.ran',
]

// Task event data (TaskEvent.data per type).

/** The normalized worker event (internal/event.Event), stored as `type: "worker"`. */
export interface WorkerEventData {
  kind:
    | 'session'
    | 'message'
    | 'tool_call'
    | 'tool_result'
    | 'file'
    | 'usage'
    | 'report'
    | 'error'
    | 'log'
    | 'background_wait'
  step?: number
  text?: string
  session?: { conversationId: string; model?: string }
  tool?: WorkerTool
  file?: { path: string; op: 'read' | 'edit'; tool?: string }
  usage?: Usage
  report?: {
    status: 'success' | 'error'
    response?: string
    error?: string
    usage?: Usage
    durationMs?: number
    deniedActions?: string[]
    synthesized?: boolean
  }
  error?: { class: 'quota_exhausted' | 'auth_required' | 'transient' | 'fatal'; message: string }
}

export interface WorkerTool {
  id?: string
  name: string
  state: 'calling' | 'done' | 'failed'
  input?: unknown
  command?: string
  output?: string
  durationMs?: number
  blocked?: boolean
}

export interface LeadMessageData {
  message: string
  by: 'lead' | 'user' | 'gate' | 'verifier'
  model?: string
  effort?: string
  tier?: Tier
}

export interface RouteData {
  accountId: string
  accountLabel?: string
  model: string
  reason: string
  continuing: boolean
  remaining?: number
}

const isObject = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null

export function workerData(event: TaskEvent): WorkerEventData | null {
  return event.type === 'worker' && typeof event.data.kind === 'string'
    ? (event.data as unknown as WorkerEventData)
    : null
}

export function leadMessageData(event: TaskEvent): LeadMessageData | null {
  return event.type === 'lead_message' && typeof event.data.message === 'string'
    ? (event.data as unknown as LeadMessageData)
    : null
}

export function routeData(event: TaskEvent): RouteData | null {
  return event.type === 'route' && typeof event.data.accountId === 'string'
    ? (event.data as unknown as RouteData)
    : null
}

export function attentionData(event: TaskEvent): Attention | { state: 'cleared' } | null {
  if (event.type !== 'attention' || typeof event.data.state !== 'string') return null
  return event.data as unknown as Attention | { state: 'cleared' }
}

/** A string field of an event's data, or undefined. */
export function dataString(event: TaskEvent, field: string): string | undefined {
  const value = event.data[field]
  return typeof value === 'string' ? value : undefined
}

/** A number field of an event's data, or undefined. */
export function dataNumber(event: TaskEvent, field: string): number | undefined {
  const value = event.data[field]
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

/** A string-array field of an event's data ([] when absent). */
export function dataStrings(event: TaskEvent, field: string): string[] {
  const value = event.data[field]
  return Array.isArray(value) ? value.filter((v): v is string => typeof v === 'string') : []
}

export function dataUsage(value: unknown): Usage | null {
  return isObject(value) && typeof value.totalTokens === 'number'
    ? (value as unknown as Usage)
    : null
}
