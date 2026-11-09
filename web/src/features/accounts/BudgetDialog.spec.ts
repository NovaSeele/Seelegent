import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { AccountsApi } from '@/api/accounts'
import type { Account } from '@/api/types'
import { useAccountsStore } from '@/stores/accounts'
import { setupApp } from '@/test/app'
import { makeAccount } from '@/test/fixtures'

import BudgetDialog from './BudgetDialog.vue'

afterEach(() => {
  document.body.innerHTML = ''
})

const key = (overrides: Partial<Account> = {}) =>
  makeAccount({
    id: 'k1',
    label: 'Work key',
    kind: 'api-key',
    provider: 'openrouter',
    ...overrides,
  })

async function open(account: Account, update: AccountsApi['update'] = vi.fn()) {
  const { plugins } = await setupApp()
  useAccountsStore().setApi({ update } as unknown as AccountsApi)
  const wrapper = mount(BudgetDialog, {
    props: { open: false, account },
    global: { plugins },
    attachTo: document.body,
  })
  await wrapper.setProps({ open: true })
  await flushPromises()
  return wrapper
}

const text = () => document.querySelector('[role="dialog"]')?.textContent ?? ''

describe('BudgetDialog', () => {
  it('says that use outside Seelegent counts toward a service-counted budget', async () => {
    await open(key({ spendSource: 'service', spentUsd: 18 }))
    expect(text()).toContain('Use of this key outside Seelegent counts toward the budget too')
    expect(text()).toContain('including use outside Seelegent')
    expect(text()).not.toContain('no prices yet')
  })

  it('says a service without prices has no cost to check, and nothing of the kind for the CLI', async () => {
    await open(key({ spendSource: 'none', spentTokens: 10 }))
    expect(text()).toContain('This service has no prices yet')
    document.body.innerHTML = ''
    await open(key({ spendSource: 'cli', spentUsd: 1 }))
    expect(text()).not.toContain('no prices yet')
    expect(text()).not.toContain('outside Seelegent')
  })

  it('saves with Enter in the amount', async () => {
    const update = vi.fn<AccountsApi['update']>((id, body) =>
      Promise.resolve(key({ id, budgetUsd: body.budgetUsd ?? 0 })),
    )
    await open(key({ spendSource: 'prices', spentUsd: 1 }), update)
    const input = document.querySelector<HTMLInputElement>(
      '[role="dialog"] input[inputmode="decimal"]',
    )
    if (!input) throw new Error('no amount field')
    input.value = '25'
    input.dispatchEvent(new Event('input'))
    input.dispatchEvent(
      new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }),
    )
    await flushPromises()
    expect(update).toHaveBeenCalledWith('k1', { budgetUsd: 25, budgetStop: true })
  })
})
