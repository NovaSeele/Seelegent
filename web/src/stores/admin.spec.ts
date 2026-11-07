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
import type { Role } from '@/api/types'
import { setupApp } from '@/test/app'
import { makeAccount, T0 } from '@/test/fixtures'

import { useAccountsStore } from './accounts'
import { useConnectionStore } from './connection'
import { createLoader } from './loader'
import { useRolesStore } from './roles'
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
  it('deletes a new API-key account again when its key cannot be stored', async () => {
    const created = makeAccount({ id: 'k1', kind: 'api-key', backend: 'claude', status: 'no_key' })
    const { client, calls } = fakeClient((method) => {
      if (method === 'POST') return { body: created }
      if (method === 'PUT') return error(400, 'invalid_argument', 'key too short')
      return { status: 204 }
    })
    const store = useAccountsStore()
    store.setApi(createAccountsApi(client))
    const result = store.create(
      { backend: 'claude', kind: 'api-key', label: 'Key', provider: 'anthropic' },
      'sk',
    )
    await expect(result).rejects.toBeInstanceOf(ApiError)
    expect(calls).toEqual([
      'POST /api/v1/accounts',
      'PUT /api/v1/accounts/k1/key',
      'DELETE /api/v1/accounts/k1',
    ])
    expect(store.get('k1')).toBeUndefined()
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

  it('shows a hook test the daemon does not support as a 501', async () => {
    const { client } = fakeClient(() => error(501, 'not_implemented'))
    const store = useSettingsStore()
    store.setApi(createSettingsApi(client))
    const run = store.testHook('h1').catch((e: unknown) => e)
    expect(await run).toMatchObject({ status: 501, code: 'not_implemented' })
  })
})
