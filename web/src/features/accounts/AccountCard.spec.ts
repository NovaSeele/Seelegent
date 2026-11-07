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

  it.each(['disabled', 'no_key', 'no_plan', 'needs_login', 'twin'] as const)(
    'leaves out the slots chip for an account that cannot run (%s)',
    async (status) => {
      const wrapper = await render(makeAccount({ status, busy: 0, maxConcurrent: 2 }))
      expect(wrapper.text()).not.toContain('slots busy')
    },
  )

  it('leaves out the slots chip for an account switched off, unless a run is still going', async () => {
    const idle = await render(makeAccount({ enabled: false, busy: 0, maxConcurrent: 2 }))
    expect(idle.text()).not.toContain('slots busy')
    const running = await render(makeAccount({ enabled: false, busy: 1, maxConcurrent: 2 }))
    expect(running.text()).toContain('1 of 2 slots busy')
    const ready = await render(makeAccount({ busy: 0, maxConcurrent: 2 }))
    expect(ready.text()).toContain('0 of 2 slots busy')
  })

  it('says a failed read with a reason once, in the quota box', async () => {
    const base = makeAccount({ status: 'error' })
    const error = {
      kind: 'unavailable',
      message: 'The quota service did not answer.',
      at: base.quota.fetchedAt ?? new Date().toISOString(),
    } as const
    const wrapper = await render({ ...base, quota: { ...base.quota, error } })
    expect(wrapper.text()).toContain('Last read failed: The quota service did not answer.')
    expect(wrapper.text()).not.toContain('the numbers below may be old')
  })

  it('shows out of quota in the pill instead of Ready, once', async () => {
    const base = makeAccount()
    const until = new Date(Date.now() + 3_600_000).toISOString()
    const wrapper = await render({ ...base, quota: { ...base.quota, exhaustedUntil: until } })
    const pill = wrapper.get('.card__head').text()
    expect(pill).toContain('Out of quota')
    expect(wrapper.text()).not.toContain('Ready')
    expect(wrapper.text().match(/Out of quota/g)).toHaveLength(1)
    expect(wrapper.find('.card__head .tone-attention').exists()).toBe(true)
  })

  it('shows a resting account as resting, in a neutral pill', async () => {
    const wrapper = await render(makeAccount({ resting: true }))
    expect(wrapper.get('.card__head').text()).toContain('Resting')
    expect(wrapper.text()).not.toContain('Ready')
    expect(wrapper.text().match(/Resting/g)).toHaveLength(1)
    expect(wrapper.find('.card__head .tone-neutral').exists()).toBe(true)
  })

  it('keeps the footer to on/off, priority on one row with its label, and the menu', async () => {
    const wrapper = await render(makeAccount())
    const foot = wrapper.get('.card__foot')
    expect(foot.find('[aria-label="Refresh quota"]').exists()).toBe(false)
    expect(foot.find('.num--inline').exists()).toBe(true)
    expect(foot.text()).toContain('Priority')
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
