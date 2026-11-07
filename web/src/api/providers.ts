/** Typed endpoints of API providers (contract §4). */
import type { ApiClient } from './client'
import type { Provider, ProviderList, ProviderPreset } from './types'

const enc = encodeURIComponent

export type ProviderChanges = Partial<Omit<ProviderPreset, 'id'>>

export function createProvidersApi(client: ApiClient) {
  return {
    list: (signal?: AbortSignal) => client.get<ProviderList>('/providers', { signal }),
    create: (body: ProviderPreset) => client.post<Provider>('/providers', { body }),
    update: (id: string, body: ProviderChanges) =>
      client.patch<Provider>(`/providers/${enc(id)}`, { body }),
    /** 409 conflict while accounts use it. */
    remove: (id: string) => client.delete<void>(`/providers/${enc(id)}`),
  }
}

export type ProvidersApi = ReturnType<typeof createProvidersApi>
