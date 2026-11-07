/**
 * The app's API client, typed endpoints and event types (contract: api-v1.md). Data shapes come from types.ts,
 * which re-exports the generated types.gen.ts; SSE frames and the TaskEvent union live in events.ts.
 */
import { i18n } from '@/i18n'

import { createAccountsApi } from './accounts'
import { createApiClient } from './client'
import { createGitApi } from './git'
import { createProofApi } from './proof'
import { createProvidersApi } from './providers'
import { createRolesApi } from './roles'
import { createSettingsApi } from './settings'
import { createTasksApi } from './tasks'
import { createUsageApi } from './usage'

export * from './client'
// The generated error-body interface is also called ApiError (as ApiErrorBody.error); here the name means the
// client's error class, and an explicit export wins over the star exports.
export { ApiError } from './client'
export type * from './types'
export * from './events'
export type { TaskListQuery, TaskEventsPage, EventsQuery, TasksApi } from './tasks'
export type { CommitResult, GitApi } from './git'
export type { AccountChanges, AccountsApi, NewAccount } from './accounts'
export type { ProofApi } from './proof'
export type { ProviderChanges, ProvidersApi } from './providers'
export type { HistoryQuery, UsageApi } from './usage'
export type { SettingsApi } from './settings'
export type { RoleFields, RolesApi, RoleWrite } from './roles'
export { EVENTS_PAGE_MAX } from './tasks'
export {
  createAccountsApi,
  createGitApi,
  createProofApi,
  createProvidersApi,
  createRolesApi,
  createSettingsApi,
  createTasksApi,
  createUsageApi,
}

export const api = createApiClient({ getLocale: () => i18n.global.locale.value })
export const tasksApi = createTasksApi(api)
export const gitApi = createGitApi(api)
export const accountsApi = createAccountsApi(api)
export const providersApi = createProvidersApi(api)
export const usageApi = createUsageApi(api)
export const settingsApi = createSettingsApi(api)
export const rolesApi = createRolesApi(api)
export const proofApi = createProofApi(api)

export const EVENTS_URL = '/api/v1/events'
