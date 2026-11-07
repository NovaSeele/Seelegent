/** Endpoints of the account, provider, usage, settings and role APIs, and the 400 field details. */
import { describe, expect, it, vi } from 'vitest'

import { createAccountsApi } from './accounts'
import { ApiError, createApiClient, fieldProblems } from './client'
import { createProvidersApi } from './providers'
import { createRolesApi } from './roles'
import { createSettingsApi } from './settings'
import { createUsageApi } from './usage'

function setup(body: unknown = {}, status = 200) {
  const fetch = vi.fn<typeof globalThis.fetch>(
    async () =>
      new Response(status === 204 ? null : JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json' },
      }),
  )
  const client = createApiClient({ fetch })
  const call = (i = 0) => {
    const [url, init] = fetch.mock.calls[i] ?? []
    return {
      url: String(url),
      method: init?.method,
      body: init?.body ? (JSON.parse(String(init.body)) as unknown) : undefined,
    }
  }
  return {
    accounts: createAccountsApi(client),
    providers: createProvidersApi(client),
    usage: createUsageApi(client),
    settings: createSettingsApi(client),
    roles: createRolesApi(client),
    call,
  }
}

describe('account endpoints', () => {
  it('escapes ids and sends the sign-in code in the body, never the URL', async () => {
    const { accounts, call } = setup({ accountId: 'a/1', state: 'waiting_code' })
    await accounts.loginCode('a/1', 'ABCD-1234')
    expect(call()).toEqual({
      url: '/api/v1/accounts/a%2F1/login/code',
      method: 'POST',
      body: { code: 'ABCD-1234' },
    })
  })

  it('maps every write to its route', async () => {
    const { accounts, call } = setup({ id: 'a1' })
    await accounts.update('a1', { priority: 3 })
    await accounts.setKey('a1', 'sk-secret')
    await accounts.clearKey('a1')
    await accounts.refreshAll()
    await accounts.logout('a1')
    expect([0, 1, 2, 3, 4].map((i) => `${call(i).method} ${call(i).url}`)).toEqual([
      'PATCH /api/v1/accounts/a1',
      'PUT /api/v1/accounts/a1/key',
      'DELETE /api/v1/accounts/a1/key',
      'POST /api/v1/quota/refresh',
      'POST /api/v1/accounts/a1/logout',
    ])
    expect(call(1).body).toEqual({ key: 'sk-secret' })
  })
})

describe('usage, roles, settings and providers', () => {
  it('reads usage with only the filters given', async () => {
    const { usage, call } = setup({})
    await usage.summary(30)
    await usage.history({ bucket: 'day', since: '2026-11-01T00:00:00.000Z' })
    expect(call().url).toBe('/api/v1/usage/summary?days=30')
    expect(call(1).url).toBe('/api/v1/usage/history?since=2026-11-01T00%3A00%3A00.000Z&bucket=day')
  })

  it('deletes a project role by key and a global one without cwd', async () => {
    const { roles, call } = setup(null, 204)
    await roles.remove('qa', 'project', 'shop-1a2b3c4d')
    await roles.remove('qa', 'global', 'ignored')
    expect(call().url).toBe('/api/v1/roles/qa?scope=project&cwd=shop-1a2b3c4d')
    expect(call(1).url).toBe('/api/v1/roles/qa?scope=global')
  })

  it('lists every project with all=1', async () => {
    const { roles, call } = setup({ items: [], projects: [] })
    await roles.list({ all: true })
    expect(call().url).toBe('/api/v1/roles?all=1')
  })

  it('patches settings with a merge patch and tests a hook by id', async () => {
    const { settings, providers, call } = setup({})
    await settings.patch({ backends: { codex: { sandbox: 'read-only' } }, minRemaining: null })
    await settings.testHook('h 1')
    await providers.update('deepseek', { enabled: false })
    expect(call()).toEqual({
      url: '/api/v1/settings',
      method: 'PATCH',
      body: { backends: { codex: { sandbox: 'read-only' } }, minRemaining: null },
    })
    expect(call(1).url).toBe('/api/v1/hooks/h%201/test')
    expect(call(2)).toMatchObject({ method: 'PATCH', url: '/api/v1/providers/deepseek' })
  })
})

describe('error details', () => {
  it('keeps details and lists the invalid fields', async () => {
    const { settings } = setup(
      {
        error: {
          code: 'invalid_argument',
          message: 'invalid settings',
          details: { fields: [{ field: 'stallMinutes', message: 'must be between 1 and 10080' }] },
        },
      },
      400,
    )
    const error = await settings.patch({ stallMinutes: 0 }).catch((e: unknown) => e)
    expect(error).toBeInstanceOf(ApiError)
    expect(fieldProblems(error)).toEqual([
      { field: 'stallMinutes', message: 'must be between 1 and 10080' },
    ])
  })

  it('reads a single details.field too, and nothing from other errors', () => {
    const single = new ApiError(400, 'invalid_argument', 'unknown tier', { field: 'tier' })
    expect(fieldProblems(single)).toEqual([{ field: 'tier', message: 'unknown tier' }])
    expect(fieldProblems(new Error('x'))).toEqual([])
  })
})
