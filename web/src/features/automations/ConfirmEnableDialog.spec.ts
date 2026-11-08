/** The confirm dialog reads the automation also when it is open from the start (the ?confirm= link, F37). */
import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { h } from 'vue'

import { createApiClient, createAutomationsApi, type AutomationsApi } from '@/api'
import type { Automation } from '@/api/types'
import { useAutomationsStore } from '@/stores/automations'
import { flush, setupApp } from '@/test/app'

import ConfirmEnableDialog from './ConfirmEnableDialog.vue'

const automation: Automation = {
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
  revision: 'r1',
  running: false,
  lastRun: null,
  createdAt: '2026-11-07T00:00:00.000Z',
  updatedAt: '2026-11-07T00:00:00.000Z',
}

describe('ConfirmEnableDialog', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  it('shows what a run does when it is mounted open', async () => {
    const { plugins } = await setupApp()
    const preview = {
      runs: ['2026-11-08T07:30:00Z', '2026-11-09T07:30:00Z', '2026-11-10T07:30:00Z'],
      local: [],
      timezone: 'local',
    }
    const fetch = vi.fn<typeof globalThis.fetch>(
      async (input) =>
        new Response(JSON.stringify(String(input).endsWith('/preview') ? preview : automation), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
    )
    useAutomationsStore().setApi(createAutomationsApi(createApiClient({ fetch })))
    mount(
      { setup: () => () => h(ConfirmEnableDialog, { open: true, automationId: 'a1' }) },
      { attachTo: document.body, global: { plugins } },
    )
    await flush()
    const dialog = document.querySelector('[role=dialog]') as HTMLElement
    await vi.waitFor(() => expect(dialog.textContent).toContain('Tidy the changelog'))
    expect(dialog.textContent).toContain('/srv/app')
    expect(dialog.textContent).toContain('Skips when less than 20% is left')
    // Its next three runs come from the daemon's preview of exactly this schedule and zone.
    await vi.waitFor(() => expect(dialog.querySelectorAll('li')).toHaveLength(3))
    const urls = fetch.mock.calls.map(([input]) => String(input))
    expect(urls.filter((u) => u.endsWith('/automations/a1'))).toHaveLength(1)
    const body = JSON.parse(
      String(fetch.mock.calls.find(([u]) => String(u).endsWith('/preview'))?.[1]?.body),
    )
    expect(body).toEqual({ schedule: automation.schedule, timezone: 'local', count: 3 })
  })

  it('drops a slow answer for the automation it was opened for before (E-B17)', async () => {
    const { plugins } = await setupApp()
    const b: Automation = { ...automation, id: 'b1', name: 'Weekly', prompt: 'Bump the deps' }
    let releaseA: (a: Automation) => void = () => undefined
    const get = vi.fn((id: string) =>
      id === 'a1' ? new Promise<Automation>((resolve) => (releaseA = resolve)) : Promise.resolve(b),
    )
    const confirmToken = vi.fn(() => Promise.resolve({ token: 'tok' }))
    const confirm = vi.fn((id: string) =>
      Promise.resolve({ ...(id === 'a1' ? automation : b), enabled: true }),
    )
    const api = {
      get,
      preview: () => Promise.resolve({ runs: [], local: [], timezone: 'local' }),
      confirmToken,
      confirm,
    } as unknown as AutomationsApi
    useAutomationsStore().setApi(api)
    const wrapper = mount(ConfirmEnableDialog, {
      props: { open: true, automationId: 'a1' },
      attachTo: document.body,
      global: { plugins },
    })
    await flush()
    await wrapper.setProps({ automationId: 'b1' })
    await flush()
    releaseA(automation)
    await flush()
    const dialog = document.querySelector('[role=dialog]') as HTMLElement
    await vi.waitFor(() => expect(dialog.textContent).toContain('Bump the deps'))
    expect(dialog.textContent).not.toContain('Tidy the changelog')

    const turnOn = [...dialog.querySelectorAll('button')].find((el) =>
      el.textContent?.includes('Turn on'),
    )
    turnOn?.click()
    await vi.waitFor(() => expect(confirm).toHaveBeenCalled())
    expect(confirmToken).toHaveBeenCalledWith('b1', 'r1')
    expect(confirm).not.toHaveBeenCalledWith('a1', expect.anything())
    wrapper.unmount()
  })
})
