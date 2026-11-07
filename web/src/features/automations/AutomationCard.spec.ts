/** An automation the user never confirmed offers no Run now that would only fail: it offers its review instead. */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import type { Automation } from '@/api/types'
import { setupApp } from '@/test/app'

import AutomationCard from './AutomationCard.vue'

const automation = (overrides: Partial<Automation>): Automation => ({
  id: 'a1',
  name: 'Nightly',
  prompt: 'Tidy the changelog',
  cwd: '/srv/app',
  schedule: { cron: '0 6 1 * *' },
  timezone: 'local',
  gate: null,
  gateCommands: [],
  lane: [],
  noLane: false,
  quota: { minRemaining: 20, useBeforeReset: false },
  graceMinutes: 60,
  enabled: false,
  enableRequested: false,
  confirmed: false,
  revision: 'r1',
  running: false,
  lastRun: null,
  createdAt: '2026-11-07T00:00:00.000Z',
  updatedAt: '2026-11-07T00:00:00.000Z',
  ...overrides,
})

async function card(a: Automation) {
  const { plugins } = await setupApp()
  return mount(AutomationCard, { props: { automation: a, busy: false }, global: { plugins } })
}

describe('AutomationCard', () => {
  it('replaces Run now with its review while the automation is unconfirmed', async () => {
    const w = await card(automation({}))
    const labels = w.findAll('button').map((b) => b.text())
    expect(labels).not.toContain('Run now')
    const review = w.findAll('button').find((b) => b.text() === 'Review and turn on')
    expect(review).toBeDefined()
    const why = review?.attributes('aria-describedby') ?? ''
    expect(w.find(`[id="${why}"]`).text()).toContain('Not confirmed')
    await review?.trigger('click')
    expect(w.emitted('confirm')).toHaveLength(1)
    // The cron expression in words, the expression itself on hover.
    expect(w.text()).toContain('6:00 AM on day 1 of every month')
  })

  it('says the next run in the same clock as the schedule', async () => {
    const w = await card(
      automation({
        enabled: true,
        confirmed: true,
        timezone: 'UTC',
        nextRun: '2026-12-01T06:00:00.000Z',
      }),
    )
    expect(w.text()).toContain('6:00 AM on day 1 of every month')
    expect(w.text()).toMatch(/Dec 1, 6:00\sAM/)
    expect(w.text()).not.toContain('06:00')
  })

  it('offers Run now once the user confirmed it', async () => {
    const w = await card(automation({ confirmed: true }))
    expect(w.findAll('button').map((b) => b.text())).toEqual(
      expect.arrayContaining(['Turn on', 'Run now']),
    )
    expect(w.text()).not.toContain('Not confirmed')
  })
})
