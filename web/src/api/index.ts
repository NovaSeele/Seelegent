/**
 * The app's API client, typed endpoints and event types (contract: api-v1.md). Data shapes live in types.ts
 * (replaced by the tygo-generated types.gen.ts in milestone 5); SSE frames and event payloads in events.ts.
 */
import { i18n } from '@/i18n'

import { createAccountsApi } from './accounts'
import { createApiClient } from './client'
import { createGitApi } from './git'
import { createTasksApi } from './tasks'

export * from './client'
export type * from './types'
export * from './events'
export type { Page, TaskListQuery, TaskEventsPage, EventsQuery, TasksApi } from './tasks'
export type { CommitResult, GitApi } from './git'
export type { AccountsApi } from './accounts'
export { EVENTS_PAGE_MAX } from './tasks'
export { createAccountsApi, createGitApi, createTasksApi }

export const api = createApiClient({ getLocale: () => i18n.global.locale.value })
export const tasksApi = createTasksApi(api)
export const gitApi = createGitApi(api)
export const accountsApi = createAccountsApi(api)

export const EVENTS_URL = '/api/v1/events'
