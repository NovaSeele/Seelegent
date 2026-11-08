import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import type { Account } from '@/api/types'
import { useAccountsStore } from '@/stores/accounts'
import { setupApp } from '@/test/app'
import { makeAccount } from '@/test/fixtures'

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
        ProvidersPanel: true,
      },
    },
  })
  const out: Record<string, string> = {}
  for (const tile of wrapper.findAll('.tile')) {
    out[tile.find('dt').text()] = tile.find('dd').text()
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
