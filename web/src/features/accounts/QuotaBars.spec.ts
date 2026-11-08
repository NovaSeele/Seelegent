import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import type { Account, Settings } from '@/api/types'
import ProgressBar from '@/components/ui/ProgressBar.vue'
import Tooltip from '@/components/ui/Tooltip.vue'
import { useConnectionStore } from '@/stores/connection'
import { setupApp } from '@/test/app'
import { makeAccount } from '@/test/fixtures'

import QuotaBars from './QuotaBars.vue'

const NOW = Date.parse('2026-11-08T10:00:00Z')
const inHours = (h: number) => new Date(NOW + h * 3_600_000).toISOString()

function agy(): Account {
  return makeAccount({
    backend: 'agy',
    quota: {
      source: 'active',
      groups: [
        {
          name: 'Gemini Models',
          windows: [
            { window: '5h', remaining: 0.62, resetAt: inHours(3) },
            { window: 'weekly', remaining: 0.1, resetAt: inHours(72) },
          ],
        },
        { name: 'Claude and GPT models', windows: [{ window: '5h', remaining: 0 }] },
        { name: 'Gemini 4 Argon', windows: [{ window: 'weekly', remaining: 0 }] },
      ],
    },
  })
}

async function render(account: Account, flash: string) {
  const { plugins } = await setupApp()
  const tier = { enabled: true, allowedModels: '' }
  const none = { fast: '', standard: '', strong: '', max: '' }
  useConnectionStore().settings = {
    backends: {
      agy: { ...tier, tiers: { ...none, fast: flash, standard: flash } },
      claude: { ...tier, tiers: none },
      codex: { ...tier, tiers: none, sandbox: 'workspace-write' },
    },
  } as unknown as Settings
  return mount(QuotaBars, { props: { account, now: NOW }, global: { plugins } })
}

describe('QuotaBars', () => {
  it('draws the routing group: 5h then weekly, % left, reset in the tooltip, +N', async () => {
    const wrapper = await render(agy(), 'gemini-3.8-flash')
    expect(wrapper.findAll('.qb__name').map((n) => n.text())).toEqual(['5h', 'Weekly'])
    expect(wrapper.findAll('.qb__value').map((n) => n.text())).toEqual(['62% left', '10% left'])
    const tips = wrapper.findAllComponents(Tooltip).map((tip) => tip.props('text'))
    expect(tips[0]).toBe('Gemini Models · 5h · resets in 3 hours')
    expect(tips[1]).toBe('Gemini Models · Weekly · resets in 3 days')
    expect(tips[2]).toBe('2 more windows, shown when the row is open')
    expect(wrapper.find('.qb__more').text()).toContain('+2')
    // Usual tones: room left is neutral, 90% used turns attention.
    const bars = wrapper.findAllComponents(ProgressBar)
    expect(bars.map((b) => b.props('tone'))).toEqual(['neutral', 'attention'])
  })

  it('never shows the drained Argon or Claude groups while a Gemini tier model routes', async () => {
    const wrapper = await render(agy(), 'gemini-3.8-flash')
    expect(wrapper.findAll('.qb__value').map((n) => n.text())).not.toContain('0% left')
    expect(wrapper.text()).not.toContain('Argon')
    expect(wrapper.text()).not.toContain('Claude')
  })

  it('falls back to the lowest of all windows in a neutral colour, and says so', async () => {
    const wrapper = await render(agy(), 'mystery-model')
    const bars = wrapper.findAllComponents(ProgressBar)
    expect(bars).toHaveLength(1)
    expect(bars[0]?.props('tone')).toBe('muted')
    expect(wrapper.find('.qb__value').classes()).toContain('tone-muted')
    // The row names the window it shows.
    expect(wrapper.find('.qb__name').text()).toBe('Claude and GPT models · 5h')
    const tip = wrapper.findAllComponents(Tooltip)[0]?.props('text')
    expect(tip).toBe('Lowest of all windows: Claude and GPT models · 5h')
  })

  it('says why there are no bars', async () => {
    const passive = await render(
      makeAccount({ kind: 'api-key', quota: { source: 'passive', groups: [] } }),
      'x',
    )
    expect(passive.text()).toBe('No quota to read')
    const fresh = await render(makeAccount({ status: 'new' }), 'x')
    expect(fresh.text()).toBe('Reading quota…')
  })
})
