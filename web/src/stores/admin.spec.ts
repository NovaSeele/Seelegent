/** The account writes, providers, usage, roles and settings stores against fake endpoints. */
import { flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import {
  ApiError,
  createAccountsApi,
  createApiClient,
  createRolesApi,
  createSettingsApi,
  createUsageApi,
} from '@/api'
import type { SettingsApi } from '@/api/settings'
import type { Memory, Role } from '@/api/types'
import { setupApp } from '@/test/app'
import { makeAccount, makeProvider, T0 } from '@/test/fixtures'

import { useAccountsStore } from './accounts'
import { useConnectionStore } from './connection'
import { createLoader } from './loader'
import { useProvidersStore } from './providers'
import { projectChoices, samePlace, useRolesStore } from './roles'
import { useSettingsStore } from './settings'
import { useUsageStore } from './usage'

type Route = (method: string, url: string, body: unknown) => { status?: number; body?: unknown }

function fakeClient(route: Route) {
  const calls: string[] = []
  const fetch = vi.fn<typeof globalThis.fetch>(async (input, init) => {
    const method = init?.method ?? 'GET'
    const url = String(input)
    calls.push(`${method} ${url}`)
    const body = init?.body ? (JSON.parse(String(init.body)) as unknown) : undefined
    const answer = route(method, url, body)
    const status = answer.status ?? 200
    return new Response(status === 204 ? null : JSON.stringify(answer.body ?? {}), {
      status,
      headers: { 'Content-Type': 'application/json' },
    })
  })
  return { client: createApiClient({ fetch }), calls }
}

const error = (status: number, code: string, message = code) => ({
  status,
  body: { error: { code, message } },
})

function role(overrides: Partial<Role> = {}): Role {
  return {
    id: 'qa',
    name: 'QA',
    description: '',
    rules: '',
    outputStyle: '',
    skills: [],
    preferredAccounts: [],
    scope: 'global',
    overridesGlobal: false,
    ...overrides,
  }
}

beforeEach(async () => {
  await setupApp()
})

describe('accounts store writes', () => {
  it('adds an API-key account in one call, with its key and the provider it made', async () => {
    const created = {
      ...makeAccount({ id: 'k1', kind: 'api-key', backend: 'claude', provider: 'anthropic' }),
      notices: [{ code: 'key_name_removed', field: 'apiKey', message: 'removed' }],
      newProvider: makeProvider({ id: 'anthropic', name: 'Anthropic', official: true }),
      providerCreated: true,
    }
    let sent: unknown
    const { client, calls } = fakeClient((method, _url, body) => {
      sent = body
      return { status: 201, body: created }
    })
    const store = useAccountsStore()
    store.setApi(createAccountsApi(client))
    const answer = await store.create({
      kind: 'api-key',
      newProvider: { preset: 'anthropic', backend: 'claude' },
      apiKey: 'sk-ant-xyz',
    })
    expect(calls).toEqual(['POST /api/v1/accounts'])
    expect(sent).toMatchObject({ apiKey: 'sk-ant-xyz', newProvider: { preset: 'anthropic' } })
    expect(answer.notices).toHaveLength(1)
    // The list keeps the account alone; the provider goes to the providers store at once.
    expect(store.get('k1')).toBeDefined()
    expect(store.get('k1')).not.toHaveProperty('newProvider')
    expect(store.get('k1')).not.toHaveProperty('notices')
    expect(useProvidersStore().items.map((p) => p.id)).toEqual(['anthropic'])
  })

  it('leaves nothing behind when the daemon refuses the key', async () => {
    const { client, calls } = fakeClient(() => ({
      status: 400,
      body: {
        error: {
          code: 'invalid_argument',
          message: 'this looks like a URL',
          details: { field: 'apiKey', reason: 'url' },
        },
      },
    }))
    const store = useAccountsStore()
    store.setApi(createAccountsApi(client))
    const failure = await store
      .create({ kind: 'api-key', provider: 'anthropic', apiKey: 'https://x' })
      .catch((e: unknown) => e)
    expect(failure).toBeInstanceOf(ApiError)
    expect(calls).toEqual(['POST /api/v1/accounts'])
    expect(store.accounts).toEqual([])
  })

  it('notes a saved key test on the account before its frame arrives', () => {
    const store = useAccountsStore()
    store.replaceAll([makeAccount({ id: 'k1', kind: 'api-key', testResult: 'not_tested' })])
    const result = {
      ok: true,
      status: 200,
      reason: 'ok' as const,
      message: '',
      models: [],
      latencyMs: 1,
    }
    store.noteTest('k1', result)
    expect(store.get('k1')?.testResult).toBe('not_tested')
    store.noteTest('k1', { ...result, saved: true })
    expect(store.get('k1')?.testResult).toBe('ok')
    expect(store.get('k1')?.testedAt).toBeDefined()
  })

  it('keeps the latest sign-in per account until it is cleared', () => {
    const store = useAccountsStore()
    store.applyLogin({ accountId: 'a1', state: 'open_url', url: 'https://example.test/x' })
    store.applyLogin({ accountId: 'a1', state: 'done' })
    expect(store.logins.a1?.state).toBe('done')
    store.clearLogin('a1')
    expect(store.logins.a1).toBeUndefined()
  })
})

describe('loader', () => {
  it('applies only the newest answer and keeps data on a failed reload', async () => {
    const answers: ((value: number) => void)[] = []
    let fail = false
    const applied: number[] = []
    const loader = createLoader(
      () =>
        fail
          ? Promise.reject(new Error('down'))
          : new Promise<number>((resolve) => answers.push(resolve)),
      (value) => applied.push(value),
    )
    const first = loader.load()
    const second = loader.load()
    answers[1]?.(2)
    answers[0]?.(1)
    await Promise.all([first, second])
    expect(applied).toEqual([2])
    fail = true
    await loader.load()
    expect(loader.status.value).toBe('ready')
    expect(loader.error.value).toBe('down')
  })
})

describe('roles store', () => {
  it('offers every known project for a project role, once per project key', () => {
    const known = [
      { key: 'shop-1a2b', cwd: 'D:\\Work\\Shop', name: 'shop' },
      { key: 'new-3c4d', cwd: 'D:/Work/new', name: 'new' },
    ]
    const withRoles = [
      // Same project as the first, spelled differently: the key says so.
      { key: 'shop-1a2b', cwd: 'd:/work/shop', name: 'shop' },
      { key: 'old-5e6f', cwd: '/srv/old', name: 'old' },
    ]
    expect(projectChoices(known, withRoles)).toEqual([
      { key: 'shop-1a2b', cwd: 'D:\\Work\\Shop', name: 'shop' },
      { key: 'new-3c4d', cwd: 'D:/Work/new', name: 'new' },
      { key: 'old-5e6f', cwd: '/srv/old', name: 'old' },
    ])
    // The first project role: GET /roles lists no project yet.
    expect(projectChoices(known, []).map((p) => p.key)).toEqual(['shop-1a2b', 'new-3c4d'])
  })

  it('treats project folders that differ only in case or separators as one place on Windows', () => {
    expect(
      samePlace(
        { id: 'qa', scope: 'project', cwd: 'D:\\Work\\Shop' },
        { id: 'qa', scope: 'project', cwd: 'd:/work/shop/' },
      ),
    ).toBe(true)
  })

  it('moves a role by writing the new place, then deleting the old one', async () => {
    const { client, calls } = fakeClient((method) =>
      method === 'GET' ? { body: { items: [], projects: [] } } : { status: 204, body: role() },
    )
    const store = useRolesStore()
    store.setApi(createRolesApi(client))
    await store.save(
      { role: { ...role(), id: 'qa' }, scope: 'project', cwd: 'D:/app' },
      { id: 'qa', scope: 'global' },
    )
    expect(calls.slice(0, 2)).toEqual([
      'POST /api/v1/roles',
      'DELETE /api/v1/roles/qa?scope=global',
    ])
  })

  it('removes the new copy when the old one cannot be deleted (old bug B26)', async () => {
    const { client, calls } = fakeClient((method, url) => {
      if (method === 'GET') return { body: { items: [], projects: [] } }
      if (method === 'POST') return { body: role() }
      if (url.endsWith('scope=global')) return error(500, 'internal')
      return { status: 204 }
    })
    const store = useRolesStore()
    store.setApi(createRolesApi(client))
    const move = store.save(
      { role: { ...role(), id: 'qa' }, scope: 'project', cwd: 'D:/app' },
      { id: 'qa', scope: 'global' },
    )
    await expect(move).rejects.toBeInstanceOf(ApiError)
    expect(calls).toContain('DELETE /api/v1/roles/qa?scope=project&cwd=D%3A%2Fapp')
  })

  it('finishes a move when the old copy is already gone (404), without deleting the new one', async () => {
    const { client, calls } = fakeClient((method, url) => {
      if (method === 'GET') return { body: { items: [], projects: [] } }
      if (method === 'POST') return { body: role() }
      if (url.endsWith('scope=global')) return error(404, 'not_found')
      return { status: 204 }
    })
    const store = useRolesStore()
    store.setApi(createRolesApi(client))
    await store.save(
      { role: { ...role(), id: 'qa' }, scope: 'project', cwd: 'D:/app' },
      { id: 'qa', scope: 'global' },
    )
    expect(calls.filter((c) => c.startsWith('DELETE'))).toEqual([
      'DELETE /api/v1/roles/qa?scope=global',
    ])
  })

  it('counts deleting a role that is already gone as done', async () => {
    const { client } = fakeClient((method) =>
      method === 'GET' ? { body: { items: [], projects: [] } } : error(404, 'not_found'),
    )
    const store = useRolesStore()
    store.setApi(createRolesApi(client))
    await expect(store.remove({ id: 'qa', scope: 'global' })).resolves.toBeUndefined()
    const { client: failing } = fakeClient((method) =>
      method === 'GET' ? { body: { items: [], projects: [] } } : error(500, 'internal'),
    )
    store.setApi(createRolesApi(failing))
    await expect(store.remove({ id: 'qa', scope: 'global' })).rejects.toBeInstanceOf(ApiError)
  })
})

describe('usage store', () => {
  it('reads hours for 7 days and days for 30, and saves the range', async () => {
    const { client, calls } = fakeClient(() => ({ body: {} }))
    const store = useUsageStore()
    store.setApi(createUsageApi(client), () => T0)
    store.ensure()
    await flushPromises()
    store.days = 30
    await flushPromises()
    const since7 = encodeURIComponent(new Date(T0 - 7 * 86_400_000).toISOString())
    expect(calls).toContain(`GET /api/v1/usage/history?since=${since7}&bucket=hour`)
    expect(calls).toContain('GET /api/v1/usage/summary?days=30')
    expect(calls.some((c) => c.endsWith('bucket=day'))).toBe(true)
    expect(localStorage.getItem('seelegent.v1.usage.range')).toBe('30')
  })
})

describe('settings store', () => {
  it('applies the saved settings and rethrows a 400 with its fields', async () => {
    let reject = false
    const { client } = fakeClient(() =>
      reject
        ? {
            status: 400,
            body: {
              error: {
                code: 'invalid_argument',
                message: 'invalid',
                details: { fields: [{ field: 'loopRepeat', message: 'between 2 and 100' }] },
              },
            },
          }
        : { body: { port: 47831, loopRepeat: 9 } },
    )
    const store = useSettingsStore()
    store.setApi(createSettingsApi(client))
    await store.save({ loopRepeat: 9 })
    expect(useConnectionStore().settings?.loopRepeat).toBe(9)
    reject = true
    await expect(store.save({ loopRepeat: 1 })).rejects.toBeInstanceOf(ApiError)
    expect(useConnectionStore().settings?.loopRepeat).toBe(9)
  })

  it('keeps the memory of the project picked last when an older read answers late', async () => {
    const answers = new Map<string, (memory: Memory) => void>()
    const store = useSettingsStore()
    store.setApi({
      memory: (cwd: string) => new Promise<Memory>((resolve) => answers.set(cwd, resolve)),
    } as unknown as SettingsApi)
    const first = store.loadMemory('D:/a')
    const second = store.loadMemory('D:/b')
    answers.get('D:/b')?.({ cwd: 'D:/b', text: 'B' })
    answers.get('D:/a')?.({ cwd: 'D:/a', text: 'A' })
    expect(await second).toMatchObject({ text: 'B' })
    expect(await first).toBeNull()
    expect(store.memory).toMatchObject({ cwd: 'D:/b', text: 'B' })
  })

  it('warns about memory changed elsewhere only when it really differs', async () => {
    const frames = new Map<string, (data: unknown) => void>()
    const resyncs: (() => void)[] = []
    const connection = useConnectionStore()
    vi.spyOn(connection, 'subscribe').mockImplementation((name, handler) => {
      frames.set(name, handler as (data: unknown) => void)
      return () => undefined
    })
    vi.spyOn(connection, 'onResync').mockImplementation((handler) => {
      resyncs.push(handler)
      return () => undefined
    })
    let onDisk: Memory = { cwd: 'D:/a', text: 'old', updatedAt: '2026-11-01T00:00:00Z' }
    const store = useSettingsStore()
    store.setApi({
      memory: () => Promise.resolve(onDisk),
      putMemory: (cwd: string, text: string) => {
        onDisk = { cwd, text, updatedAt: '2026-11-02T00:00:00Z' }
        return Promise.resolve(onDisk)
      },
    } as unknown as SettingsApi)
    await store.loadMemory('D:/a')
    await store.saveMemory('D:/a', 'mine')
    // The frame of our own save: same text and time as held.
    frames.get('memory.changed')?.({ cwd: 'd:\\a' })
    await flushPromises()
    expect(store.memoryChanged).toBe(false)
    // Another project's frame is ignored; someone else's save to this one warns.
    onDisk = { cwd: 'D:/a', text: 'theirs', updatedAt: '2026-11-03T00:00:00Z' }
    frames.get('memory.changed')?.({ cwd: 'D:/other' })
    await flushPromises()
    expect(store.memoryChanged).toBe(false)
    frames.get('memory.changed')?.({ cwd: 'D:/a' })
    await flushPromises()
    expect(store.memoryChanged).toBe(true)
    // After a resync the memory is checked again as well.
    await store.loadMemory('D:/a')
    expect(store.memoryChanged).toBe(false)
    onDisk = { cwd: 'D:/a', text: 'later', updatedAt: '2026-11-04T00:00:00Z' }
    resyncs.forEach((handler) => handler())
    await flushPromises()
    expect(store.memoryChanged).toBe(true)
  })

  /** A daemon whose PUT answers only when told, storing text the way Go's memory.Store.Replace does. */
  function slowMemoryDaemon() {
    const frames = new Map<string, (data: unknown) => void>()
    vi.spyOn(useConnectionStore(), 'subscribe').mockImplementation((name, handler) => {
      frames.set(name, handler as (data: unknown) => void)
      return () => undefined
    })
    const disk: { now: Memory } = {
      now: { cwd: 'D:/a', text: 'old\n', updatedAt: '2026-11-01T00:00:00Z' },
    }
    let answerPut: (() => void) | undefined
    const store = useSettingsStore()
    store.setApi({
      memory: () => Promise.resolve(disk.now),
      putMemory: (cwd: string, text: string) => {
        // The daemon writes and publishes memory.changed, then answers the PUT.
        const stored = text.trim() === '' ? '' : text.endsWith('\n') ? text : `${text}\n`
        disk.now = { cwd, text: stored, updatedAt: '2026-11-02T00:00:00Z' }
        const answer = disk.now
        return new Promise<Memory>((resolve) => (answerPut = () => resolve(answer)))
      },
    } as unknown as SettingsApi)
    return {
      store,
      disk,
      frame: () => frames.get('memory.changed')?.({ cwd: 'D:/a' }),
      answer: () => answerPut?.(),
    }
  }

  it('does not warn about our own save when its frame, with normalised text, comes before the PUT answers', async () => {
    const { store, frame, answer } = slowMemoryDaemon()
    await store.loadMemory('D:/a')
    const save = store.saveMemory('D:/a', 'mine')
    frame()
    await flushPromises()
    expect(store.memoryChanged).toBe(false)
    answer()
    await save
    await flushPromises()
    expect(store.memoryChanged).toBe(false)
    expect(store.memory).toMatchObject({ text: 'mine\n' })
  })

  it('still warns when someone else saves after our PUT, while it is answering', async () => {
    const { store, disk, frame, answer } = slowMemoryDaemon()
    await store.loadMemory('D:/a')
    const save = store.saveMemory('D:/a', 'mine')
    disk.now = { cwd: 'D:/a', text: 'theirs\n', updatedAt: '2026-11-03T00:00:00Z' }
    frame()
    await flushPromises()
    answer()
    await save
    await flushPromises()
    expect(store.memoryChanged).toBe(true)
  })

  it('shows a hook test the daemon does not support as a 501', async () => {
    const { client } = fakeClient(() => error(501, 'not_implemented'))
    const store = useSettingsStore()
    store.setApi(createSettingsApi(client))
    const run = store.testHook('h1').catch((e: unknown) => e)
    expect(await run).toMatchObject({ status: 501, code: 'not_implemented' })
  })
})
