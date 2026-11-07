import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import type { Account } from '@/api/types'
import { useAccountsStore } from '@/stores/accounts'
import { setupApp } from '@/test/app'
import { makeAccount } from '@/test/fixtures'

import AccountCard from './AccountCard.vue'

async function render(account: Account, others: Account[] = []) {
  const { plugins } = await setupApp()
  useAccountsStore().replaceAll([account, ...others])
  return mount(AccountCard, { props: { account, now: Date.now() }, global: { plugins } })
}

describe('AccountCard', () => {
  it.each([
    ['ok', 'Ready'],
    ['new', 'Checking quota'],
    ['needs_login', 'Sign-in required'],
    ['no_key', 'No API key'],
    ['no_plan', 'Plan cannot use this backend'],
    ['disabled', 'Off'],
  ] as const)('words status %s as the contract does', async (status, text) => {
    const wrapper = await render(makeAccount({ status }))
    expect(wrapper.text()).toContain(text)
  })

  it('keeps an account whose last quota read failed ready, with a note', async () => {
    const wrapper = await render(makeAccount({ status: 'error' }))
    expect(wrapper.text()).toContain('Ready')
    expect(wrapper.text()).toContain('the numbers below may be old')
  })

  it('names the twin by its label', async () => {
    const twin = makeAccount({ id: 'a2', label: 'Personal' })
    const wrapper = await render(makeAccount({ status: 'twin', twinOf: 'a2' }), [twin])
    expect(wrapper.text()).toContain('Same sign-in as Personal')
  })

  it('offers the next step its status asks for', async () => {
    const login = await render(makeAccount({ status: 'needs_login' }))
    expect(login.findAll('button').map((b) => b.text())).toContain('Sign in')
    const key = await render(
      makeAccount({ status: 'no_key', kind: 'api-key', backend: 'claude', provider: 'deepseek' }),
    )
    expect(key.findAll('button').map((b) => b.text())).toContain('Set API key')
    expect(key.text()).toContain('API key · deepseek')
  })
})
