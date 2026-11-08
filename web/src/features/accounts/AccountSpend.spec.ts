import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import { setupApp } from '@/test/app'
import { makeAccount } from '@/test/fixtures'

import AccountSpend from './AccountSpend.vue'

const key = (overrides: Parameters<typeof makeAccount>[0] = {}) =>
  makeAccount({ kind: 'api-key', backend: 'claude', provider: 'anthropic', ...overrides })

async function render(overrides: Parameters<typeof makeAccount>[0], compact = true) {
  const { plugins } = await setupApp()
  return mount(AccountSpend, { props: { account: key(overrides), compact }, global: { plugins } })
}

describe('AccountSpend', () => {
  it('shows the estimate against the budget with a bar, and marks it as the CLI’s estimate', async () => {
    const w = await render({ spendSource: 'cli', spentUsd: 12.4, budgetUsd: 200 })
    expect(w.find('.spend__text').text()).toBe('≈ $12.40 / $200')
    expect(w.findComponent({ name: 'Tooltip' }).props('text')).toBe(
      "Claude Code's own estimate, not the service's bill",
    )
    expect(w.find('[role="progressbar"]').exists()).toBe(true)
    expect(w.text()).not.toContain('Set budget')
  })

  it('offers "Set budget" when there is none, and emits it', async () => {
    const w = await render({
      spendSource: 'cli',
      spentUsd: 12.4,
      spentSince: '2026-11-03T08:00:00Z',
    })
    expect(w.find('.spend__text').text()).toBe('≈ $12.40 since Nov 3')
    await w.find('.spend__link').trigger('click')
    expect(w.emitted('budget')).toHaveLength(1)
    expect(w.find('[role="progressbar"]').exists()).toBe(false)
  })

  it('says the full sentence outside the list, over budget too', async () => {
    const w = await render({ spendSource: 'cli', spentUsd: 210, budgetUsd: 200 }, false)
    expect(w.find('.spend__text').text()).toBe('≈ $210 spent of $200 · $10 over')
  })

  it('shows tokens when the cost is not reported, and free for a local service', async () => {
    const tokens = await render({ spendSource: 'none', spentTokens: 1_200_000 })
    expect(tokens.text()).toContain('Cost not reported · 1.2M tokens')
    const unknown = await render({ spendSource: 'none', spentTokens: 10, budgetUsd: 50 }, false)
    expect(unknown.text()).toContain('Budget $50 is not enforced: this service reports no cost')
    const free = await render({ spendSource: 'free' })
    expect(free.text()).toBe('Local · free')
  })
})
