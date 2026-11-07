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
import type { Hook, HookRun, Memory, Settings, SettingsPatch } from '@/api/types'
import { pathKey } from '@/lib/paths'

import { useConnectionStore } from './connection'
import { createLoader } from './loader'

/** A saved hook with its last run (from hook.ran frames and tests since the list was read). */
export interface HookView extends Hook {
  lastRun: HookRun | null
}

export const useSettingsStore = defineStore('settings', () => {
  let client: SettingsApi = defaultApi
  const hooks = shallowRef<readonly HookView[]>([])
  /** GET /hooks answered 501: this daemon has no hooks yet, so there is nothing to retry or add. */
  const hooksUnsupported = shallowRef(false)
  const hooksLoader = createLoader(
    async (signal): Promise<readonly HookView[] | null> => {
      try {
        return withRuns(await client.hooks(signal))
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
  /**
   * The memory write in flight and its answer (null if it fails), so its own memory.changed frame, which can come
   * before the PUT answers, is compared with what the daemon stored rather than with the text sent.
   */
  let saving: { cwd: string; done: Promise<Memory | null> } | null = null
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

  /** Keeps the last runs of the hooks that stay (by id). */
  function withRuns(list: readonly Hook[]): HookView[] {
    const runs = new Map(hooks.value.map((h) => [h.id, h.lastRun]))
    return list.map((h) => ({ ...h, lastRun: runs.get(h.id) ?? null }))
  }

  async function saveHooks(list: readonly Hook[]): Promise<void> {
    hooks.value = withRuns(await client.putHooks(list))
  }

  function applyRun(run: HookRun): void {
    hooks.value = hooks.value.map((hook) =>
      hook.id === run.hookId ? { ...hook, lastRun: run } : hook,
    )
  }

  /** Runs one hook now and keeps the run as its last one. */
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
    const put = client.putMemory(cwd, text)
    const write = {
      cwd,
      done: put.then(
        (stored) => stored,
        () => null,
      ),
    }
    saving = write
    try {
      const next = await put
      if (mine === memorySeq) {
        memory.value = next
        memoryChanged.value = false
      }
      return next
    } finally {
      if (saving === write) saving = null
    }
  }

  /**
   * After a memory.changed frame (for `cwd`) or a resync: reads the held project's memory again and marks it
   * changed elsewhere only when the text or the save time differ from what is held. Our own save never warns:
   * the daemon publishes memory.changed while it handles the PUT, so the frame (and this read) can come before
   * the PUT answers. While a PUT for the project is in flight the text is not compared (the daemon normalises it,
   * so it need not equal the text sent): the check waits for the PUT and compares with its answer.
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
    if (seq !== memorySeq) return
    if (saving && pathKey(saving.cwd) === pathKey(held.cwd)) await saving.done
    const current = memory.value
    if (seq !== memorySeq || !current || pathKey(current.cwd) !== pathKey(held.cwd)) return
    if (latest.text !== current.text || latest.updatedAt !== current.updatedAt) {
      memoryChanged.value = true
    }
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
