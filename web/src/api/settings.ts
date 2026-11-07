/**
 * Typed endpoints of the daemon's settings, project memory and hooks (contract §5). PATCH /settings takes a JSON
 * merge patch and is all-or-nothing: a 400 names every invalid field in details.fields.
 */
import type { ApiClient } from './client'
import type { Hook, HookConfig, HookRun, Memory, Settings, SettingsPatch } from './types'

export function createSettingsApi(client: ApiClient) {
  return {
    get: (signal?: AbortSignal) => client.get<Settings>('/settings', { signal }),
    patch: (patch: SettingsPatch) => client.patch<Settings>('/settings', { body: patch }),
    memory: (cwd: string, signal?: AbortSignal) =>
      client.get<Memory>('/memory', { signal, query: { cwd } }),
    /** Replaces the project's memory. */
    putMemory: (cwd: string, text: string) =>
      client.put<Memory>('/memory', { body: { cwd, text } }),
    hooks: (signal?: AbortSignal) => client.get<Hook[]>('/hooks', { signal }),
    /** Replaces the hook list: the whole list, each entry with its stable id ('' for a new one). */
    putHooks: (hooks: readonly HookConfig[]) =>
      client.put<Hook[]>('/hooks', { body: { hooks: [...hooks] } }),
    /** Runs one hook by id (never by list index). 501 not_implemented until the hook runner lands. */
    testHook: (id: string) =>
      client.post<HookRun>(`/hooks/${encodeURIComponent(id)}/test`, { body: {} }),
  }
}

export type SettingsApi = ReturnType<typeof createSettingsApi>
