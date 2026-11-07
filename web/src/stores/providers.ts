/**
 * API providers (GET /providers) and their presets. Read when a page first needs them, again on every
 * providers.changed frame (old bug B10: the frame was never listened to) and after a resync.
 */
import { defineStore } from 'pinia'
import { shallowRef } from 'vue'

import { providersApi as defaultApi } from '@/api'
import type { ProviderChanges, ProvidersApi } from '@/api/providers'
import type { Provider, ProviderPreset } from '@/api/types'

import { useConnectionStore } from './connection'
import { createLoader } from './loader'

export const useProvidersStore = defineStore('providers', () => {
  let client: ProvidersApi = defaultApi
  const items = shallowRef<readonly Provider[]>([])
  const presets = shallowRef<readonly ProviderPreset[]>([])
  const { status, error, load } = createLoader(
    (signal) => client.list(signal),
    (list) => {
      items.value = list.items
      presets.value = list.presets
    },
  )
  let watching = false

  function setApi(next: ProvidersApi): void {
    client = next
  }

  /** Loads once and keeps the list current from then on. */
  function ensure(): void {
    if (watching) return
    watching = true
    const connection = useConnectionStore()
    connection.subscribe('providers.changed', () => void load())
    connection.onResync(() => void load())
    void load()
  }

  async function create(body: ProviderPreset): Promise<Provider> {
    const provider = await client.create(body)
    await load()
    return provider
  }

  async function update(id: string, changes: ProviderChanges): Promise<Provider> {
    const provider = await client.update(id, changes)
    items.value = items.value.map((p) => (p.id === id ? provider : p))
    return provider
  }

  /** 409 conflict while accounts use the provider. */
  async function remove(id: string): Promise<void> {
    await client.remove(id)
    items.value = items.value.filter((p) => p.id !== id)
  }

  return { items, presets, status, error, setApi, ensure, load, create, update, remove }
})
