import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import { setupApp } from '@/test/app'

import QuotaBands from './QuotaBands.vue'

const t0 = new Date(2026, 10, 1, 15).getTime()
const t1 = new Date(2026, 10, 4, 15).getTime()
const series = [
  { key: 'gemini-pro · 5h', points: [{ t: t0, v: 0.8 }], low: 0.8 },
  { key: 'gemini-pro · weekly', points: [{ t: t0, v: 0.5 }], low: 0.5 },
]

describe('QuotaBands', () => {
  it('draws one band per window over a shared axis with a gridline per day', async () => {
    const { plugins } = await setupApp()
    const wrapper = mount(QuotaBands, {
      props: { series, t0, t1, minRemaining: 0.1 },
      global: { plugins },
    })
    const bands = wrapper.findAll('.qb__band')
    expect(bands).toHaveLength(2)
    expect(bands[0]?.text()).toContain('gemini-pro · 5h: lowest 80%')
    // Midnights of Nov 2, 3 and 4 in every band, and one shared row of day labels.
    expect(bands[0]?.findAll('.qb__grid')).toHaveLength(3)
    expect(wrapper.findAll('.qb__ticks span')).toHaveLength(3)
    expect(wrapper.findAll('.qb__min').length).toBeGreaterThan(0)
  })
})
