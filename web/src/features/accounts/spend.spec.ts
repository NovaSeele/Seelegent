import { describe, expect, it } from 'vitest'

import { makeAccount } from '@/test/fixtures'

import { spendView } from './spend'

const key = (overrides: Parameters<typeof makeAccount>[0] = {}) =>
  makeAccount({ kind: 'api-key', backend: 'claude', provider: 'anthropic', ...overrides })

describe('spendView', () => {
  it('has nothing to say about a sign-in account', () => {
    expect(spendView(makeAccount({ kind: 'isolated' }))).toBeNull()
  })

  it('shows the CLI estimate against the budget, warning from 80% and in danger from 100%', () => {
    const at = (spentUsd: number, budgetState?: 'ok' | 'warn' | 'reached') =>
      spendView(
        key({
          spendSource: 'cli',
          spentUsd,
          budgetUsd: 200,
          ...(budgetState ? { budgetState } : {}),
        }),
      )
    expect(at(12.4)).toMatchObject({ kind: 'cost', spent: 12.4, budget: 200, tone: 'neutral' })
    expect(at(12.4)?.ratio).toBeCloseTo(0.062)
    expect(at(160)?.tone).toBe('attention')
    expect(at(200)?.tone).toBe('danger')
    expect(at(250)?.ratio).toBeCloseTo(1.25)
    // The daemon's own state wins when it is further along (it counts what the CLI has not reported yet).
    expect(at(10, 'warn')?.tone).toBe('attention')
    expect(at(10, 'reached')?.tone).toBe('danger')
  })

  it('shows the amount since the key was set when there is no budget', () => {
    const view = spendView(
      key({ spendSource: 'cli', spentUsd: 12.4, spentSince: '2026-11-03T08:00:00Z' }),
    )
    expect(view).toMatchObject({
      kind: 'cost',
      since: '2026-11-03T08:00:00Z',
      budgetUnknown: false,
    })
    expect(view?.budget).toBeUndefined()
    expect(view?.ratio).toBeUndefined()
  })

  it('shows only tokens until the service has prices, and says a budget there waits for them', () => {
    const none = spendView(key({ spendSource: 'none', spentTokens: 1_200_000, budgetUsd: 50 }))
    expect(none).toMatchObject({
      kind: 'tokens',
      tokens: 1_200_000,
      budget: 50,
      budgetUnknown: true,
      needsPrices: true,
      estimate: false,
    })
    expect(
      spendView(key({ spendSource: 'none', spentTokens: 9, spentUnpricedTokens: 4 }))?.tokens,
    ).toBe(4)
    expect(none?.ratio).toBeUndefined()
    expect(spendView(key({ spentTokens: 5 }))?.kind).toBe('tokens')
  })

  it('reads a local service as free', () => {
    expect(spendView(key({ spendSource: 'free' }))).toMatchObject({
      kind: 'free',
      budgetUnknown: false,
    })
  })

  it('marks only the CLI’s amount as an estimate, and an unknown source as none', () => {
    expect(spendView(key({ spendSource: 'cli' }))?.estimate).toBe(true)
    expect(spendView(key({ spendSource: 'prices', spentUsd: 2 }))).toMatchObject({
      source: 'prices',
      kind: 'cost',
      estimate: false,
      needsPrices: false,
      tokens: 0,
    })
    expect(
      spendView(key({ spendSource: 'prices', spentUsd: 2, spentUnpricedTokens: 30 }))?.tokens,
    ).toBe(30)
    expect(
      spendView(key({ spendSource: 'later' as unknown as 'none', spentTokens: 3 })),
    ).toMatchObject({ source: 'none', kind: 'tokens' })
  })

  it('offers the service’s own limit as the budget only while none is set', () => {
    const service = (budgetUsd?: number) =>
      spendView(
        key({
          spendSource: 'service',
          spentUsd: 1,
          suggestedBudgetUsd: 25,
          ...(budgetUsd ? { budgetUsd } : {}),
        }),
      )
    expect(service()?.suggested).toBe(25)
    expect(service(10)?.suggested).toBeUndefined()
    expect(service(10)?.ratio).toBeCloseTo(0.1)
  })
})
