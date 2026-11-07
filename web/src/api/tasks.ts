/**
 * Typed endpoints of the task API (contract §2 /state, §3.1-§3.2). Built on an ApiClient so tests can pass a
 * client with a fake fetch.
 */
import type { ApiClient } from './client'
import type { TaskEvent } from './events'
import type {
  BootState,
  EventPage,
  FollowupRequest,
  Project,
  PromptText,
  TaskDetail,
  TaskPage,
  TaskStatus,
  TaskSummary,
  Verdict,
} from './types'

export interface TaskListQuery {
  status?: readonly TaskStatus[]
  project?: string
  q?: string
  /** 1..200 (default 50). */
  limit?: number
  cursor?: string
}

/** EventPage with its items typed as the TaskEvent union (the generator only knows the envelope). */
export type TaskEventsPage = Omit<EventPage, 'items'> & { items: TaskEvent[] }

/** `after` + `limit` reads forward from a seq; `tail` reads the newest N. Never both. */
export type EventsQuery = { after: number; limit?: number } | { tail: number }

export const EVENTS_PAGE_MAX = 2000

const enc = encodeURIComponent

export function createTasksApi(client: ApiClient) {
  return {
    bootState: (signal?: AbortSignal) => client.get<BootState>('/state', { signal }),

    projects: () => client.get<Project[]>('/projects'),

    list(query: TaskListQuery = {}, signal?: AbortSignal) {
      return client.get<TaskPage>('/tasks', {
        signal,
        query: {
          status: query.status?.length ? query.status.join(',') : undefined,
          project: query.project || undefined,
          q: query.q?.trim() || undefined,
          limit: query.limit,
          cursor: query.cursor,
        },
      })
    },

    get: (id: string, signal?: AbortSignal) =>
      client.get<TaskDetail>(`/tasks/${enc(id)}`, { signal }),

    events(id: string, query: EventsQuery, signal?: AbortSignal) {
      return client.get<TaskEventsPage>(`/tasks/${enc(id)}/events`, {
        signal,
        query: 'tail' in query ? { tail: query.tail } : { after: query.after, limit: query.limit },
      })
    },

    /** The full worker brief of an attempt (latest when `attempt` is omitted); events carry a preview. */
    prompt: (id: string, turn: number, attempt?: number) =>
      client.get<PromptText>(`/tasks/${enc(id)}/prompt`, { query: { turn, attempt } }),

    followup: (id: string, body: FollowupRequest) =>
      client.post<TaskSummary>(`/tasks/${enc(id)}/followup`, { body }),

    /** answerObjection answers a peer's open objection (required to accept over one). */
    review: (id: string, verdict: Verdict, notes?: string, answerObjection?: string) =>
      client.post<TaskSummary>(`/tasks/${enc(id)}/review`, {
        body: {
          verdict,
          ...(notes?.trim() ? { notes: notes.trim() } : {}),
          ...(answerObjection?.trim() ? { answerObjection: answerObjection.trim() } : {}),
        },
      }),

    /** 200 when it stopped within `waitSeconds`, 202 while it is still stopping; both return the summary. */
    cancel: (id: string, waitSeconds = 0) =>
      client.post<TaskSummary>(`/tasks/${enc(id)}/cancel`, {
        body: waitSeconds ? { waitSeconds } : {},
      }),

    answer: (id: string, answer: string) =>
      client.post<TaskSummary>(`/tasks/${enc(id)}/answer`, { body: { answer } }),

    dismissAttention: (id: string) =>
      client.post<TaskSummary>(`/tasks/${enc(id)}/attention/dismiss`, { body: {} }),

    takeover: (id: string) => client.post<TaskSummary>(`/tasks/${enc(id)}/takeover`, { body: {} }),
  }
}

export type TasksApi = ReturnType<typeof createTasksApi>
