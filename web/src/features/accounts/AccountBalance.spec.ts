import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import type { Balance } from '@/api/types'
import { useProvidersStore } from '@/stores/providers'
import { setupApp } from '@/test/app'
import { makeAccount, makeProvider } from '@/test/fixtures'

import AccountBalance from './AccountBalance.vue'

const balance = (overrides: Partial<Balance> = {}): Balance => ({
  amount: 187.6,
  currency: 'CNY',
  accountWide: true,
  available: true,
  readAt: new Date(Date.now() - 120_000).toISOString(),
  stale: false,
  ...overrides,
})

async function render(overrides: Parameters<typeof makeAccount>[0], compact = false) {
  const { plugins } = await setupApp()
  useProvidersStore().items = [makeProvider({ id: 'deepseek', name: 'DeepSeek' })]
  const account = makeAccount({ kind: 'api-key', provider: 'deepseek', ...overrides })
  return mount(AccountBalance, { props: { account, compact }, global: { plugins } })
}

const tip = (w: Awaited<ReturnType<typeof render>>) =>
  w.findComponent({ name: 'Tooltip' }).props('text') as string

describe('AccountBalance', () => {
  it('says whose balance it is and when it was read, with paid and granted in the tooltip', async () => {
    const w = await render({ balance: balance({ paid: 150, granted: 37.6 }) })
    expect(w.find('.bal__amount').text()).toBe('Balance ¥187.60')
    expect(w.find('.bal__detail').text()).toBe('(account-wide, read 2 minutes ago)')
    expect(tip(w)).toBe(
      'Balance of the whole DeepSeek account, shared by all its keys · Paid ¥150.00 · Granted ¥37.60',
    )
  })

  it('keeps the detail in the tooltip in the list, and lists other currencies there', async () => {
    const w = await render(
      {
        balance: balance({
          currency: 'USD',
          amount: 4.2,
          other: [{ amount: 30, currency: 'CNY' }],
        }),
      },
      true,
    )
    expect(w.text()).toBe('Balance $4.20')
    expect(tip(w)).toContain('¥30.00')
    expect(tip(w)).toContain('account-wide, read 2 minutes ago')
  })

  it('marks a stale reading and one that cannot pay', async () => {
    expect((await render({ balance: balance({ stale: true }) })).text()).toContain(
      'may be out of date',
    )
    const empty = await render({ balance: balance({ amount: 0, available: false }) })
    expect(empty.text()).toContain('cannot pay for requests')
    expect(empty.find('.bal--off').exists()).toBe(true)
  })

  it('shows a failed reading with its reason and keeps the service’s message as the title', async () => {
    const w = await render({
      serviceError: {
        reason: 'refused',
        message: 'api.deepseek.com: 401 Unauthorized',
        at: '2026-11-09T08:00:00Z',
      },
    })
    const line = w.find('.bal--error')
    expect(line.text()).toBe('Reading the spend: the service refused the key')
    expect(line.attributes('title')).toBe('api.deepseek.com: 401 Unauthorized')
    expect(
      (await render({ serviceError: { reason: 'no_credit', message: '', at: '' } }, true)).text(),
    ).toBe('')
  })

  it('writes an unknown currency code as sent', async () => {
    const w = await render({ balance: balance({ currency: 'credits', amount: 3 }) })
    expect(w.text()).toContain('Balance 3.00 credits')
  })
})
