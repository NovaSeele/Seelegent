import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import type { Account } from '@/api/types'
import { useAccountsStore } from '@/stores/accounts'
import { setupApp } from '@/test/app'
import { makeAccount } from '@/test/fixtures'

import AccountFacts from './AccountFacts.vue'

async function render(account: Account, hasSession = false, others: Account[] = []) {
  const { plugins } = await setupApp()
  useAccountsStore().replaceAll([account, ...others])
  return mount(AccountFacts, {
    props: { account, now: Date.now(), hasSession },
    global: { plugins },
  })
}

describe('AccountFacts', () => {
  it('shows signing in only while the card has no sign-in panel of its own', async () => {
    const elsewhere = await render(makeAccount({ signingIn: true }))
    expect(elsewhere.text()).toContain('Signing in')
    const here = await render(makeAccount({ signingIn: true }), true)
    expect(here.text()).not.toContain('Signing in')
  })

  it('shows resting as a chip only when the pill shows a status word that is not resting', async () => {
    const off = await render(makeAccount({ status: 'disabled', enabled: false, resting: true }))
    expect(off.text()).toContain('Resting')
    const ready = await render(makeAccount({ resting: true }))
    expect(ready.text()).not.toContain('Resting')
  })

  it('names the twin in the twin note', async () => {
    const main = makeAccount({ id: 'a0', label: 'Work Pro' })
    const wrapper = await render(makeAccount({ status: 'twin', twinOf: 'a0' }), false, [main])
    expect(wrapper.text()).toContain('the same sign-in as Work Pro')
  })
})
