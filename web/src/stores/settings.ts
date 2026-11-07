/**
 * Writes to the daemon's settings, plus the hooks list and project memory the settings dialog edits.
 *
 * The settings themselves live in the connection store (GET /state and SSE settings.updated). `save` sends a
 * merge patch and applies the answer at once; a 400 is thrown as is, and `fieldProblems(error)` names the fields.
 * Hooks are read when the hooks section opens and after each hook.ran frame (which updates one hook's last run);
 * memory is read per project (newest read wins), and a memory.changed frame for that project, or a resync, reads it
 * again and marks it changed elsewhere when it differs from what is held.
 */
import { defineStore } from 'pinia'
import { shallowRef } from 'vue'

import { settingsApi as defaultApi } from '@/api'
import { ApiError } from '@/api/client'
import type { SettingsApi } from '@/api/settings'
import type { Hook, HookConfig, HookRun, Memory, Settings, SettingsPatch } from '@/api/types'
import { pathKey } from '@/lib/paths'

import { useConnectionStore } from './connection'
import { createLoader } from './loader'

export const useSettingsStore = defineStore('settings', () => {
  let client: SettingsApi = defaultApi
  const hooks = shallowRef<readonly Hook[]>([])
  /** GET /hooks answered 501: this daemon has no hooks yet, so there is nothing to retry or add. */
  const hooksUnsupported = shallowRef(false)
  const hooksLoader = createLoader(
    async (signal): Promise<readonly Hook[] | null> => {
      try {
        return await client.hooks(signal)
      } catch (cause) {
        if (cause instanceof ApiError && cause.status === 501) return null
        throw cause
      }
    },
    (list) => {
      hooksUnsupported.value = list === null
      hooks.value = list ?? []
    },
  )
  /** The project folder whose memory is loaded, its text, and whether another writer changed it since. */
  const memory = shallowRef<Memory | null>(null)
  const memoryChanged = shallowRef(false)
  /** Bumped by every memory read and write; an answer applies only while it is the newest. */
  let memorySeq = 0
  let watching = false

  function setApi(next: SettingsApi): void {
    client = next
  }

  function watchFrames(): void {
    if (watching) return
    watching = true
    const connection = useConnectionStore()
    connection.subscribe('hook.ran', (run) => applyRun(run))
    connection.subscribe('memory.changed', ({ cwd }) => void checkMemory(cwd))
    connection.onResync(() => {
      if (hooksLoader.status.value !== 'idle') void hooksLoader.load()
      void checkMemory()
    })
  }

  async function save(patch: SettingsPatch): Promise<Settings> {
    const next = await client.patch(patch)
    useConnectionStore().settings = next
    return next
  }

  function loadHooks(): Promise<void> {
    watchFrames()
    return hooksLoader.load()
  }

  async function saveHooks(list: readonly HookConfig[]): Promise<void> {
    hooks.value = await client.putHooks(list)
  }

  function applyRun(run: HookRun): void {
    hooks.value = hooks.value.map((hook) =>
      hook.id === run.hookId ? { ...hook, lastRun: run } : hook,
    )
  }

  /** Runs one hook now; 501 not_implemented until the daemon's hook runner lands. */
  async function testHook(id: string): Promise<HookRun> {
    const run = await client.testHook(id)
    applyRun(run)
    return run
  }

  /**
   * Reads a project's memory. Only the newest read or write applies: an older answer (the project switched
   * meanwhile) resolves to null and changes nothing, and an older failure is dropped the same way.
   */
  async function loadMemory(cwd: string): Promise<Memory | null> {
    watchFrames()
    const mine = ++memorySeq
    try {
      const next = await client.memory(cwd)
      if (mine !== memorySeq) return null
      memory.value = next
      memoryChanged.value = false
      return next
    } catch (cause) {
      if (mine !== memorySeq) return null
      throw cause
    }
  }

  async function saveMemory(cwd: string, text: string): Promise<Memory> {
    const mine = ++memorySeq
    const next = await client.putMemory(cwd, text)
    if (mine === memorySeq) {
      memory.value = next
      memoryChanged.value = false
    }
    return next
  }

  /**
   * After a memory.changed frame (for `cwd`) or a resync: reads the held project's memory again and marks it
   * changed elsewhere only when the text or the save time differ from what is held. Our own save's frame
   * therefore never warns.
   */
  async function checkMemory(cwd?: string): Promise<void> {
    const held = memory.value
    if (!held || (cwd !== undefined && pathKey(cwd) !== pathKey(held.cwd))) return
    const seq = memorySeq
    let latest: Memory
    try {
      latest = await client.memory(held.cwd)
    } catch {
      return
    }
    if (seq !== memorySeq || memory.value !== held) return
    if (latest.text !== held.text || latest.updatedAt !== held.updatedAt) memoryChanged.value = true
  }

  return {
    hooks,
    hooksStatus: hooksLoader.status,
    hooksError: hooksLoader.error,
    hooksUnsupported,
    memory,
    memoryChanged,
    setApi,
    save,
    loadHooks,
    saveHooks,
    testHook,
    loadMemory,
    saveMemory,
  }
})
