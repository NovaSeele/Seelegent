import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import type { AccountsApi } from '@/api/accounts'
import { useAccountsStore } from '@/stores/accounts'
import { useProvidersStore } from '@/stores/providers'
import { setupApp } from '@/test/app'
import { makeAccount, makeProvider } from '@/test/fixtures'

import AccountSpend from './AccountSpend.vue'

const key = (overrides: Parameters<typeof makeAccount>[0] = {}) =>
  makeAccount({ kind: 'api-key', backend: 'claude', provider: 'anthropic', ...overrides })

async function render(overrides: Parameters<typeof makeAccount>[0], compact = true) {
  const { plugins } = await setupApp()
  return mount(AccountSpend, { props: { account: key(overrides), compact }, global: { plugins } })
}

const tip = (w: Awaited<ReturnType<typeof render>>) =>
  w.findComponent({ name: 'Tooltip' }).props('text') as string

describe('AccountSpend', () => {
  it('shows the estimate against the budget with a bar, and marks it as the CLI’s estimate', async () => {
    const w = await render({ spendSource: 'cli', spentUsd: 12.4, budgetUsd: 200 })
    expect(w.find('.spend__text').text()).toBe('≈ $12.40 / $200')
    expect(tip(w)).toBe("Claude Code's own estimate, not the service's bill")
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

  it('shows tokens with "Set prices" for a service without prices, and free for a local one', async () => {
    const tokens = await render({ spendSource: 'none', spentUnpricedTokens: 1_200_000 })
    expect(tokens.find('.spend__text').text()).toBe('1.2M tokens')
    await tokens.find('.spend__link').trigger('click')
    expect(tokens.emitted('prices')).toHaveLength(1)
    expect(tokens.text()).toContain('Set prices to see cost')
    const unknown = await render({ spendSource: 'none', spentTokens: 10, budgetUsd: 50 }, false)
    expect(unknown.text()).toContain(
      "Budget $50 waits for prices: set this service's prices to judge it",
    )
    const free = await render({ spendSource: 'free' })
    expect(free.text()).toBe('Local · free')
  })

  it('counts at the user’s prices without "≈", and names the tokens of models without a price', async () => {
    const w = await render(
      { spendSource: 'prices', spentUsd: 3.5, spentUnpricedTokens: 40_000 },
      false,
    )
    expect(w.find('.spend__text').text()).toBe('$3.50 spent')
    expect(tip(w)).toBe('At your prices for this service')
    expect(w.text()).toContain('40K tokens without a price')
    await w.findAll('.spend__link').at(-1)?.trigger('click')
    expect(w.emitted('prices')).toHaveLength(1)
  })

  it('shows the service’s own count with the periods and the limit in the tooltip', async () => {
    const w = await render({
      spendSource: 'service',
      spentUsd: 7.25,
      spentSince: '2026-11-03T08:00:00Z',
      budgetUsd: 20,
      serviceUsage: {
        totalUsd: 7.25,
        dailyUsd: 0.5,
        weeklyUsd: 2,
        monthlyUsd: 7.25,
        limitUsd: 20,
        limitReset: 'monthly',
        readAt: '2026-11-09T08:00:00Z',
        stale: true,
      },
    })
    expect(w.find('.spend__text').text()).toBe('$7.25 / $20')
    expect(tip(w)).toBe(
      "anthropic's count for this key, including use outside Seelegent · " +
        'Counted by the service for this key: $7.25 · today $0.50 · this week $2 · this month $7.25 · ' +
        'limit $20, resets monthly · may be out of date',
    )
  })

  it('offers the key’s OpenRouter limit as the budget in one click', async () => {
    const { plugins } = await setupApp()
    const update = vi.fn<AccountsApi['update']>((id, body) =>
      Promise.resolve(key({ id, budgetUsd: body.budgetUsd ?? 0 })),
    )
    useAccountsStore().setApi({ update } as unknown as AccountsApi)
    const w = mount(AccountSpend, {
      props: {
        account: key({ id: 'or1', spendSource: 'service', spentUsd: 1, suggestedBudgetUsd: 25 }),
        compact: true,
      },
      global: { plugins },
    })
    const link = w.find('.spend__link')
    expect(link.text()).toBe("Use the key's OpenRouter limit, $25")
    await link.trigger('click')
    await flushPromises()
    expect(update).toHaveBeenCalledWith('or1', { budgetUsd: 25, budgetStop: true })
  })

  it('shows an account-wide balance beside the spend, in the currency sent', async () => {
    const w = await render(
      {
        spendSource: 'prices',
        spentUsd: 1,
        balance: {
          amount: 187.6,
          currency: 'CNY',
          accountWide: true,
          available: true,
          readAt: new Date(Date.now() - 120_000).toISOString(),
          stale: false,
        },
      },
      false,
    )
    expect(w.find('.bal__amount').text()).toBe('Balance ¥187.60')
    expect(w.find('.bal__detail').text()).toBe('(account-wide, read 2 minutes ago)')
  })

  it('leaves the links out when plain (the budget dialog)', async () => {
    const { plugins } = await setupApp()
    const w = mount(AccountSpend, {
      props: { account: key({ spendSource: 'none', spentTokens: 5 }), compact: false, plain: true },
      global: { plugins },
    })
    expect(w.find('.spend__text').text()).toBe('5 tokens, no cost known')
    expect(w.find('.spend__link').exists()).toBe(false)
  })

  it('says in full that the service counts the key’s use outside Seelegent too', async () => {
    const { plugins } = await setupApp()
    useProvidersStore().items = [makeProvider({ id: 'openrouter', name: 'OpenRouter' })]
    const w = mount(AccountSpend, {
      props: { account: key({ provider: 'openrouter', spendSource: 'service', spentUsd: 9 }) },
      global: { plugins },
    })
    expect(w.find('.spend__note').text()).toBe(
      "OpenRouter's count for this key, including use outside Seelegent",
    )
    expect(tip(w)).toBe("OpenRouter's count for this key, including use outside Seelegent")
    const cli = await render({ spendSource: 'cli', spentUsd: 1 }, false)
    expect(cli.find('.spend__note').exists()).toBe(false)
  })
})
