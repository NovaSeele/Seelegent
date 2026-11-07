/**
 * The automations (milestone 15): the list, read when the page first needs it and kept live over
 * automation.updated (the new record, patched in place) and automation.deleted, and after a resync.
 *
 * Enabling is two steps on purpose (docs/automations.md): `requestEnable` records the request (the lead's tools can
 * do that much), and `confirm` fetches a one-time token and spends it at once. Only this page sends the
 * dashboard-only header both confirm calls need.
 */
import { defineStore } from 'pinia'
import { shallowRef } from 'vue'

import { automationsApi as defaultApi } from '@/api'
import type { AutomationsApi } from '@/api/github'
import type { Automation, AutomationInput, AutomationPatch } from '@/api/types'

import { useConnectionStore } from './connection'
import { createLoader } from './loader'
import { upsertById } from './team'

export const useAutomationsStore = defineStore('automations', () => {
  let client: AutomationsApi = defaultApi
  const list = shallowRef<readonly Automation[]>([])
  const loader = createLoader(
    (signal) => client.list(signal),
    (items) => (list.value = items),
  )
  let watching = false

  function setApi(next: AutomationsApi): void {
    client = next
  }

  function api(): AutomationsApi {
    return client
  }

  function put(x: Automation): Automation {
    list.value = upsertById(list.value, x)
    return x
  }

  function ensure(): void {
    if (!watching) {
      watching = true
      const connection = useConnectionStore()
      connection.subscribe('automation.updated', (x) => put(x))
      connection.subscribe(
        'automation.deleted',
        (d) => (list.value = list.value.filter((x) => x.id !== d.id)),
      )
      connection.onResync(() => void loader.load())
    }
    if (loader.status.value === 'idle') void loader.load()
  }

  const create = async (input: AutomationInput) => put(await client.create(input))
  const patch = async (id: string, p: AutomationPatch) => put(await client.patch(id, p))

  async function remove(id: string): Promise<void> {
    await client.remove(id)
    list.value = list.value.filter((x) => x.id !== id)
  }

  /** Records the request to enable `id` (its content is what the user then confirms). */
  async function requestEnable(id: string): Promise<Automation> {
    return put((await client.requestEnable(id)).automation)
  }

  /**
   * Confirms `shown`, the automation exactly as the page shows it: a fresh one-time token for its revision, spent
   * at once. The daemon refuses when the automation changed since it was shown.
   */
  async function confirm(shown: Pick<Automation, 'id' | 'revision'>): Promise<Automation> {
    const { token } = await client.confirmToken(shown.id, shown.revision)
    return put(await client.confirm(shown.id, token))
  }

  return {
    list,
    status: loader.status,
    error: loader.error,
    load: loader.load,
    setApi,
    api,
    ensure,
    put,
    create,
    patch,
    remove,
    requestEnable,
    confirm,
  }
})
