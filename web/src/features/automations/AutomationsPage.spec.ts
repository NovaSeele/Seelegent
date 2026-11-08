/** Run now asks first, as the old dashboard did: a run hands work to a worker at once (bugs.md B6). */
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'

import { createApiClient, createAutomationsApi } from '@/api'
import type { Automation } from '@/api/types'
import { useAutomationsStore } from '@/stores/automations'
import { setupApp } from '@/test/app'

import AutomationsPage from './AutomationsPage.vue'

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
  enabled: true,
  enableRequested: false,
  confirmed: true,
  revision: 'r1',
  running: false,
  lastRun: null,
  createdAt: '2026-11-07T00:00:00.000Z',
  updatedAt: '2026-11-07T00:00:00.000Z',
}

const run = { id: 1, at: '2026-11-08T00:00:00.000Z', trigger: 'manual', outcome: 'started' }

describe('AutomationsPage', () => {
  let unmount: () => void = () => {}
  beforeEach(() => {
    document.body.innerHTML = ''
  })
  afterEach(() => unmount())

  async function open() {
    const { plugins } = await setupApp()
    const fetch = vi.fn<typeof globalThis.fetch>(
      async (input) =>
        new Response(JSON.stringify(String(input).endsWith('/run') ? run : [automation]), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
    )
    useAutomationsStore().setApi(createAutomationsApi(createApiClient({ fetch })))
    const empty = { render: () => null }
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/automations', name: 'automations', component: empty },
        { path: '/settings/:section?', name: 'settings', component: empty },
      ],
    })
    await router.push('/automations')
    await router.isReady()
    const wrapper = mount(AutomationsPage, {
      attachTo: document.body,
      global: { plugins: [...plugins, router] },
    })
    unmount = () => wrapper.unmount()
    await flushPromises()
    const runs = () => fetch.mock.calls.filter(([u]) => String(u).endsWith('/automations/a1/run'))
    return { wrapper, runs }
  }

  const dialog = () => document.querySelector<HTMLElement>('[role=dialog]')
  const button = (root: ParentNode, text: string) =>
    [...root.querySelectorAll('button')].find((b) => b.textContent?.trim() === text)

  it('runs only after the user confirms', async () => {
    const { wrapper, runs } = await open()
    const runNow = wrapper.findAll('button').find((b) => b.text() === 'Run now')
    expect(runNow).toBeDefined()
    await runNow?.trigger('click')
    await flushPromises()
    const asked = dialog()
    expect(asked?.textContent).toContain('Run Nightly now?')
    expect(asked?.textContent).toContain('its last run is still going this one is skipped')
    expect(runs()).toHaveLength(0)

    button(asked as HTMLElement, 'Run now')?.click()
    await flushPromises()
    expect(runs()).toHaveLength(1)
    expect(dialog()).toBeNull()
  })

  it('runs nothing when the user cancels', async () => {
    const { wrapper, runs } = await open()
    await wrapper
      .findAll('button')
      .find((b) => b.text() === 'Run now')
      ?.trigger('click')
    await flushPromises()
    button(dialog() as HTMLElement, 'Cancel')?.click()
    await flushPromises()
    expect(dialog()).toBeNull()
    expect(runs()).toHaveLength(0)
  })
})
