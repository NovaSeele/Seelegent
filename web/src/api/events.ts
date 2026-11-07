/**
 * Event shapes of contracts/api-v1.md that tygo cannot generate: the TaskEvent union (§3.2), built from the data
 * types in types.ts, and the SSE frames of /api/v1/events (§7).
 *
 * `isTaskEvent` checks a value against the union: the envelope, a known `type` and the required keys of that
 * type's data (EVENT_REQUIRED). It is the drift check for the daemon's fixtures
 * (internal/api/testdata/events/<type>.json). Readers use `parseTaskEvent`, which also logs an unknown `type`
 * once and drops it, so an older dashboard never breaks on a newer daemon.
 */
import type {
  Account,
  AccountFailureData,
  AttemptEndData,
  AttentionEventData,
  CancelData,
  DepsData,
  ErrorData,
  HandbackData,
  HandoffData,
  HookRun,
  LeadMessageData,
  LoginSession,
  ReattachData,
  RetryData,
  ReviewData,
  RouteData,
  Settings,
  TakeoverData,
  TaskEventBase,
  TaskSummary,
  TurnEndData,
  WaitingQuotaData,
  WorkAtRiskData,
  WorkerEvent,
  WorkerPromptData,
} from './types'

type Ev<T extends string, D> = TaskEventBase & { type: T; data: D }

export type TaskEvent =
  | Ev<'worker', WorkerEvent>
  | Ev<'lead_message', LeadMessageData>
  | Ev<'route', RouteData>
  | Ev<'worker_prompt', WorkerPromptData>
  | Ev<'attempt_end', AttemptEndData>
  | Ev<'turn_end', TurnEndData>
  | Ev<'quota_exhausted', AccountFailureData>
  | Ev<'auth_failed', AccountFailureData>
  | Ev<'waiting_quota', WaitingQuotaData>
  | Ev<'retry', RetryData>
  | Ev<'handoff', HandoffData>
  | Ev<'reattach', ReattachData>
  | Ev<'resumed', ReattachData>
  | Ev<'attention', AttentionEventData>
  | Ev<'deps', DepsData>
  | Ev<'takeover', TakeoverData>
  | Ev<'handback', HandbackData>
  | Ev<'review', ReviewData>
  | Ev<'cancel', CancelData>
  | Ev<'work_at_risk', WorkAtRiskData>
  | Ev<'error', ErrorData>

export type TaskEventType = TaskEvent['type']

/** The event of one type. */
export type TaskEventOf<T extends TaskEventType> = Extract<TaskEvent, { type: T }>

type Kind = 'string' | 'number' | 'boolean' | 'array' | 'object'

/** Required data keys per type, with their JSON kind. */
export const EVENT_REQUIRED: Readonly<Record<TaskEventType, readonly (readonly [string, Kind])[]>> =
  {
    worker: [['kind', 'string']],
    lead_message: [
      ['message', 'string'],
      ['by', 'string'],
    ],
    route: [
      ['accountId', 'string'],
      ['accountLabel', 'string'],
      ['model', 'string'],
      ['reason', 'string'],
      ['continuing', 'boolean'],
    ],
    worker_prompt: [
      ['length', 'number'],
      ['preview', 'string'],
    ],
    attempt_end: [['status', 'string']],
    turn_end: [
      ['status', 'string'],
      ['files', 'array'],
    ],
    quota_exhausted: [
      ['accountId', 'string'],
      ['error', 'string'],
    ],
    auth_failed: [
      ['accountId', 'string'],
      ['error', 'string'],
    ],
    waiting_quota: [],
    retry: [
      ['delayMs', 'number'],
      ['error', 'string'],
    ],
    handoff: [
      ['from', 'string'],
      ['to', 'string'],
      ['reason', 'string'],
    ],
    reattach: [['accountId', 'string']],
    resumed: [['accountId', 'string']],
    attention: [['state', 'string']],
    deps: [['state', 'string']],
    takeover: [
      ['state', 'string'],
      ['accountId', 'string'],
    ],
    handback: [
      ['turn', 'number'],
      ['response', 'string'],
    ],
    review: [['verdict', 'string']],
    cancel: [],
    work_at_risk: [['files', 'array']],
    error: [['message', 'string']],
  }

export const TASK_EVENT_TYPES = Object.keys(EVENT_REQUIRED) as TaskEventType[]

const WORKER_KINDS: ReadonlySet<string> = new Set([
  'session',
  'message',
  'tool_call',
  'tool_result',
  'file',
  'usage',
  'report',
  'error',
  'log',
  'background_wait',
])

/** An attention event that sets attention (not `cleared`) also carries reason, who and since. */
const ATTENTION_SET: readonly (readonly [string, Kind])[] = [
  ['reason', 'string'],
  ['who', 'string'],
  ['since', 'string'],
]

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value)

function hasKind(value: unknown, kind: Kind): boolean {
  if (kind === 'array') return Array.isArray(value)
  if (kind === 'object') return isRecord(value)
  if (kind === 'number') return typeof value === 'number' && Number.isFinite(value)
  return typeof value === kind
}

export const isKnownEventType = (type: unknown): type is TaskEventType =>
  typeof type === 'string' && Object.hasOwn(EVENT_REQUIRED, type)

/** Why a value is not a TaskEvent ('' when it is one). */
export function taskEventProblem(value: unknown): string {
  if (!isRecord(value)) return 'not an object'
  if (!Number.isInteger(value.seq) || (value.seq as number) < 1) return 'seq'
  if (typeof value.ts !== 'string') return 'ts'
  if (typeof value.taskId !== 'string') return 'taskId'
  if (value.turn !== undefined && typeof value.turn !== 'number') return 'turn'
  if (value.attempt !== undefined && typeof value.attempt !== 'number') return 'attempt'
  if (!isKnownEventType(value.type)) return `unknown type ${String(value.type)}`
  const data = value.data
  if (!isRecord(data)) return 'data'
  const required = [...EVENT_REQUIRED[value.type]]
  if (value.type === 'attention' && data.state !== 'cleared') required.push(...ATTENTION_SET)
  for (const [key, kind] of required) {
    if (!hasKind(data[key], kind)) return `data.${key}`
  }
  if (value.type === 'worker' && !WORKER_KINDS.has(data.kind as string)) return 'data.kind'
  return ''
}

export function isTaskEvent(value: unknown): value is TaskEvent {
  return taskEventProblem(value) === ''
}

/**
 * Returns the event when it is valid. An unknown type is logged once per type and dropped; a malformed event of
 * a known type is logged once per type and problem.
 */
export function createEventParser(log: (message: string) => void = console.warn) {
  const seen = new Set<string>()
  return function parseTaskEvent(value: unknown): TaskEvent | null {
    const problem = taskEventProblem(value)
    if (!problem) return value as TaskEvent
    const type = isRecord(value) ? String(value.type) : '?'
    const key = isKnownEventType(type) ? `${type}:${problem}` : `type:${type}`
    if (!seen.has(key)) {
      seen.add(key)
      log(`ignoring task event (${problem})${isKnownEventType(type) ? ` of type ${type}` : ''}`)
    }
    return null
  }
}

// SSE frames (§7).

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
  'hook.ran': HookRun
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
