/** The confirm dialog reads the automation also when it is open from the start (the ?confirm= link, F37). */
import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { h } from 'vue'

import { createApiClient, createAutomationsApi } from '@/api'
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
    const fetch = vi.fn<typeof globalThis.fetch>(
      async () =>
        new Response(JSON.stringify(automation), {
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
    expect(fetch).toHaveBeenCalledTimes(1)
    const dialog = document.querySelector('[role=dialog]') as HTMLElement
    await vi.waitFor(() => expect(dialog.textContent).toContain('Tidy the changelog'))
    expect(dialog.textContent).toContain('/srv/app')
  })
})
