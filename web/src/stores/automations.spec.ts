/** The automations store against a fake endpoint: the enable request and the dashboard's confirmation. */
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { createApiClient, createAutomationsApi } from '@/api'
import type { Automation } from '@/api/types'
import { setupApp } from '@/test/app'

import { useAutomationsStore } from './automations'

interface Call {
  method: string
  url: string
  body: unknown
  headers: Record<string, string>
}

function fakeClient(answer: (call: Call) => unknown) {
  const calls: Call[] = []
  const fetch = vi.fn<typeof globalThis.fetch>(async (input, init) => {
    const call: Call = {
      method: init?.method ?? 'GET',
      url: String(input),
      body: init?.body ? (JSON.parse(String(init.body)) as unknown) : undefined,
      headers: { ...(init?.headers as Record<string, string>) },
    }
    calls.push(call)
    return new Response(JSON.stringify(answer(call) ?? {}), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  })
  return { client: createApiClient({ fetch }), calls }
}

const automation = (overrides: Partial<Automation> = {}): Automation => ({
  id: 'a1',
  name: 'Nightly',
  prompt: 'Tidy the changelog',
  cwd: '/srv/app',
  schedule: { every: 'daily', at: '07:30' },
  timezone: 'local',
  gate: null,
  gateCommands: [],
  lane: [],
  noLane: false,
  quota: { minRemaining: 20, useBeforeReset: false },
  graceMinutes: 60,
  enabled: false,
  enableRequested: true,
  confirmed: false,
  revision: 'r-shown',
  running: false,
  lastRun: null,
  createdAt: '2026-11-07T00:00:00.000Z',
  updatedAt: '2026-11-07T00:00:00.000Z',
  ...overrides,
})

beforeEach(async () => {
  await setupApp()
})

describe('automations store', () => {
  it('confirms exactly the revision shown, with the dashboard header on both calls', async () => {
    const { client, calls } = fakeClient((call) =>
      call.url.endsWith('/confirm-token')
        ? { token: 't0k', expiresAt: '2026-11-07T00:05:00.000Z' }
        : automation({ enabled: true, enableRequested: false }),
    )
    const store = useAutomationsStore()
    store.setApi(createAutomationsApi(client))
    const on = await store.confirm(automation())
    expect(on.enabled).toBe(true)
    expect(calls.map((c) => `${c.method} ${c.url}`)).toEqual([
      'POST /api/v1/automations/a1/confirm-token',
      'POST /api/v1/automations/a1/confirm',
    ])
    expect(calls[0]?.body).toEqual({ revision: 'r-shown' })
    expect(calls[1]?.body).toEqual({ token: 't0k' })
    for (const c of calls) expect(c.headers['X-Seelegent-UI']).toBe('1')
    expect(store.list.map((a) => a.enabled)).toEqual([true])
  })

  it('only asks when it requests enabling: no token, no dashboard header', async () => {
    const { client, calls } = fakeClient(() => ({
      automation: automation(),
      confirm: true,
      confirmUrl: '/automations?confirm=a1',
      message: '',
    }))
    const store = useAutomationsStore()
    store.setApi(createAutomationsApi(client))
    const a = await store.requestEnable('a1')
    expect(a.enabled).toBe(false)
    expect(calls).toHaveLength(1)
    expect(calls[0]?.url).toBe('/api/v1/automations/a1/enable')
    expect(calls[0]?.headers['X-Seelegent-UI']).toBeUndefined()
  })
})
