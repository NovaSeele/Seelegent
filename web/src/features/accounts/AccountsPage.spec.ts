import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { Account } from '@/api/types'
import { useAccountsStore } from '@/stores/accounts'
import { setupApp } from '@/test/app'
import { makeAccount } from '@/test/fixtures'
import { storageKey } from '@/lib/storage'

import AccountsPage from './AccountsPage.vue'
import { readyForWork } from './accountView'

async function tiles(list: Account[]): Promise<Record<string, string>> {
  const { plugins } = await setupApp()
  useAccountsStore().replaceAll(list)
  const wrapper = mount(AccountsPage, {
    global: {
      plugins,
      stubs: {
        AccountCard: true,
        AccountDialogs: true,
        AddAccountDialog: true,
        ServicesSection: true,
      },
    },
  })
  const out: Record<string, string> = {}
  for (const chip of wrapper.findAll('.chip')) {
    out[chip.find('dt').text()] = chip.find('dd').text()
  }
  return out
}

describe('AccountsPage summary', () => {
  it('does not count an account whose slots are all busy as ready for work (A-L16)', async () => {
    const got = await tiles([makeAccount({ id: 'a1', busy: 1, maxConcurrent: 1 })])
    expect(got['Ready for work']).toBe('0')
    expect(got['Running turns']).toBe('1')
  })

  it('counts an account with a free slot', async () => {
    const got = await tiles([
      makeAccount({ id: 'a1', busy: 1, maxConcurrent: 2 }),
      makeAccount({ id: 'a2', busy: 0, maxConcurrent: 1 }),
      makeAccount({ id: 'a3', busy: 2, maxConcurrent: 2 }),
    ])
    expect(got['Ready for work']).toBe('2')
    expect(got['Running turns']).toBe('3')
  })
})

describe('readyForWork', () => {
  const now = Date.now()
  it.each([
    ['free slot', makeAccount({ busy: 0, maxConcurrent: 1 }), true],
    ['old quota numbers', makeAccount({ status: 'error', busy: 0 }), true],
    ['all slots busy', makeAccount({ busy: 2, maxConcurrent: 2 }), false],
    ['off', makeAccount({ enabled: false }), false],
    ['resting', makeAccount({ resting: true }), false],
    ['signed out', makeAccount({ status: 'needs_login' }), false],
    [
      'out of quota',
      makeAccount({
        quota: {
          source: 'active',
          groups: [],
          exhaustedUntil: new Date(now + 60_000).toISOString(),
        },
      }),
      false,
    ],
  ] as const)('%s', (_, account, want) => {
    expect(readyForWork(account, now)).toBe(want)
  })
})

describe('AccountsPage view', () => {
  beforeEach(() => localStorage.clear())
  afterEach(() => vi.restoreAllMocks())

  async function page() {
    const { plugins } = await setupApp()
    useAccountsStore().replaceAll([makeAccount({ id: 'a1' })])
    return mount(AccountsPage, {
      global: {
        plugins,
        stubs: {
          AccountCard: true,
          AccountList: true,
          AccountDialogs: true,
          AddAccountDialog: true,
          ServicesSection: true,
        },
      },
    })
  }

  const radio = (w: Awaited<ReturnType<typeof page>>, name: string) =>
    w.findAll('[role="radio"]').find((r) => r.text() === name)

  it('defaults to the list, next to "Refresh all quota"', async () => {
    const w = await page()
    expect(w.find('[role="radiogroup"]').attributes('aria-label')).toBe('Accounts view')
    expect(radio(w, 'List')?.attributes('aria-checked')).toBe('true')
    expect(w.findComponent({ name: 'AccountList' }).exists()).toBe(true)
    expect(w.findComponent({ name: 'AccountCard' }).exists()).toBe(false)
  })

  it('switches to cards and remembers it', async () => {
    const w = await page()
    await radio(w, 'Cards')?.trigger('click')
    expect(w.findComponent({ name: 'AccountCard' }).exists()).toBe(true)
    expect(w.findComponent({ name: 'AccountList' }).exists()).toBe(false)
    expect(localStorage.getItem(storageKey('accounts.view'))).toBe('"cards"')

    const again = await page()
    expect(radio(again, 'Cards')?.attributes('aria-checked')).toBe('true')
    expect(again.findComponent({ name: 'AccountCard' }).exists()).toBe(true)
  })

  it('ignores a stored value it does not know', async () => {
    localStorage.setItem(storageKey('accounts.view'), '"table"')
    const w = await page()
    expect(radio(w, 'List')?.attributes('aria-checked')).toBe('true')
  })

  it('works when storage throws on every read and write', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('denied', 'SecurityError')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('full', 'QuotaExceededError')
    })
    const w = await page()
    expect(radio(w, 'List')?.attributes('aria-checked')).toBe('true')
    await radio(w, 'Cards')?.trigger('click')
    expect(radio(w, 'Cards')?.attributes('aria-checked')).toBe('true')
    expect(w.findComponent({ name: 'AccountCard' }).exists()).toBe(true)
  })
})
