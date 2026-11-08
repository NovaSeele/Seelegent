/**
 * Sign-out and turning an account off ask first, as the old dashboard did (bugs.md B6). Turning off asks only when it
 * changes something now: turns run on the account, or no other account of its backend and provider takes its work.
 */
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, ref } from 'vue'

import type { AccountsApi } from '@/api/accounts'
import type { Account } from '@/api/types'
import { useAccountsStore } from '@/stores/accounts'
import { setupApp } from '@/test/app'
import { makeAccount } from '@/test/fixtures'

import AccountCard from './AccountCard.vue'
import AccountDialogs from './AccountDialogs.vue'
import type { AccountDialog } from './useAccountControls'
import { offConsequence } from './accountView'

describe('offConsequence', () => {
  const now = Date.parse('2026-11-08T10:00:00Z')
  const work = makeAccount({ id: 'a1' })
  const outOfQuota: Partial<Account> = {
    quota: { source: 'active', groups: [], exhaustedUntil: '2026-11-08T11:00:00Z' },
  }
  it('lets the switch flip when nothing runs and another account takes the work', () => {
    expect(offConsequence(work, [work, makeAccount({ id: 'a2' })], now)).toBeNull()
  })
  it('asks while turns run on the account', () => {
    const busy = { ...work, busy: 2 }
    expect(offConsequence(busy, [busy, makeAccount({ id: 'a2' })], now)).toEqual({
      running: 2,
      only: false,
    })
  })
  it.each([
    ['another backend', { backend: 'claude' }],
    ['another provider', { kind: 'api-key', provider: 'deepseek' }],
    ['an account that is off', { enabled: false }],
    ['a signed-out account', { status: 'needs_login' }],
    ['a second sign-in', { status: 'twin' }],
    ['a resting account', { resting: true }],
    ['an account out of quota', outOfQuota],
  ] as const)('does not count %s as the other account', (_, other) => {
    const peer = makeAccount({ id: 'a2', ...other })
    expect(offConsequence(work, [work, peer], now)).toEqual({ running: 0, only: true })
  })
  it('counts an account whose quota came back', () => {
    const peer = makeAccount({
      id: 'a2',
      quota: { source: 'active', groups: [], exhaustedUntil: '2026-11-08T09:00:00Z' },
    })
    expect(offConsequence(work, [work, peer], now)).toBeNull()
  })
  it('does not ask for an account that cannot take work anyway', () => {
    const out = makeAccount({ id: 'a1', status: 'no_plan' })
    expect(offConsequence(out, [out], now)).toBeNull()
  })
})

describe('account confirmations', () => {
  let unmount: () => void = () => {}
  beforeEach(() => {
    document.body.innerHTML = ''
  })
  afterEach(() => unmount())

  async function render(account: Account, others: Account[] = []) {
    const { plugins } = await setupApp()
    const store = useAccountsStore()
    const api = {
      logout: vi.fn(async (id: string) => ({ ...account, id })),
      update: vi.fn(async (id: string, body: { enabled?: boolean }) => ({
        ...account,
        id,
        ...body,
      })),
    }
    store.setApi(api as unknown as AccountsApi)
    store.replaceAll([account, ...others])
    const Host = defineComponent(() => {
      const dialog = ref<AccountDialog | null>(null)
      return () => [
        h(AccountCard, {
          account: store.get(account.id) ?? account,
          now: Date.now(),
          onDialog: (kind: AccountDialog) => (dialog.value = kind),
        }),
        h(AccountDialogs, {
          account: store.get(account.id),
          modelValue: dialog.value,
          'onUpdate:modelValue': (v: AccountDialog | null) => (dialog.value = v),
        }),
      ]
    })
    const wrapper = mount(Host, { attachTo: document.body, global: { plugins } })
    unmount = () => wrapper.unmount()
    await flushPromises()
    return { wrapper, api }
  }

  const dialog = () => document.querySelector<HTMLElement>('[role=dialog]')
  const button = (root: ParentNode, text: string) => {
    const found = [...root.querySelectorAll<HTMLButtonElement>('button')].find(
      (b) => b.textContent?.trim() === text,
    )
    if (!found) throw new Error(`no button "${text}"`)
    return found
  }
  const openMenu = () =>
    document.querySelector<HTMLButtonElement>('button[aria-haspopup="menu"]')?.click()
  const menuItem = async (text: string) => {
    openMenu()
    await flushPromises()
    const item = [...document.querySelectorAll<HTMLElement>('[role=menuitem]')].find(
      (el) => el.textContent?.trim() === text,
    )
    if (!item) throw new Error(`no menu item "${text}"`)
    item.click()
    await flushPromises()
  }

  it('signs out only after the user confirms', async () => {
    const { api } = await render(makeAccount({ label: 'Work' }))
    await menuItem('Sign out')
    expect(dialog()?.textContent).toContain('Sign out of Work?')
    expect(api.logout).not.toHaveBeenCalled()
    button(dialog() as HTMLElement, 'Cancel').click()
    await flushPromises()
    expect(dialog()).toBeNull()
    expect(api.logout).not.toHaveBeenCalled()

    await menuItem('Sign out')
    button(dialog() as HTMLElement, 'Sign out').click()
    await flushPromises()
    expect(api.logout).toHaveBeenCalledWith('a1')
    expect(dialog()).toBeNull()
  })

  it('offers no sign-out for an account that uses this computer’s sign-in', async () => {
    await render(makeAccount({ kind: 'system' }))
    openMenu()
    await flushPromises()
    const items = [...document.querySelectorAll('[role=menuitem]')].map((el) =>
      el.textContent?.trim(),
    )
    expect(items).toContain('Sign in')
    expect(items).not.toContain('Sign out')
  })

  it('turns off at once when another account takes the work and nothing runs', async () => {
    const { wrapper, api } = await render(makeAccount(), [makeAccount({ id: 'a2' })])
    await wrapper.get('[role=switch]').trigger('click')
    await flushPromises()
    expect(dialog()).toBeNull()
    expect(api.update).toHaveBeenCalledWith('a1', { enabled: false })
  })

  it('asks before turning off the last account of its backend, and says what waits', async () => {
    const { wrapper, api } = await render(makeAccount({ label: 'Work', busy: 1 }))
    await wrapper.get('[role=switch]').trigger('click')
    await flushPromises()
    const text = dialog()?.textContent ?? ''
    expect(text).toContain('Turn off work for Work?')
    expect(text).toContain('The task running on it finishes its turn.')
    expect(text).toContain('only Antigravity account that can take work')
    expect(api.update).not.toHaveBeenCalled()

    button(dialog() as HTMLElement, 'Turn off work').click()
    await flushPromises()
    expect(api.update).toHaveBeenCalledWith('a1', { enabled: false })
    expect(dialog()).toBeNull()
  })

  it('names the provider and says the work moves on when other accounts remain', async () => {
    const key = { kind: 'api-key', backend: 'claude', provider: 'deepseek' } as const
    const { wrapper, api } = await render(makeAccount({ ...key, busy: 2 }), [
      makeAccount({ ...key, id: 'a2' }),
    ])
    await wrapper.get('[role=switch]').trigger('click')
    await flushPromises()
    const text = dialog()?.textContent ?? ''
    expect(text).toContain('The 2 tasks running on it finish their turns.')
    expect(text).toContain('go to another Claude Code (deepseek) account')
    button(dialog() as HTMLElement, 'Cancel').click()
    await flushPromises()
    expect(api.update).not.toHaveBeenCalled()
  })

  it('sets the priority from the dialog a narrow list row asks for', async () => {
    const { plugins } = await setupApp()
    const store = useAccountsStore()
    const account = makeAccount({ label: 'Work', priority: 1 })
    const api = {
      update: vi.fn(async (id: string, body: { priority?: number }) => ({ ...account, ...body })),
    }
    store.setApi(api as unknown as AccountsApi)
    store.replaceAll([account])
    const wrapper = mount(AccountDialogs, {
      props: { account, modelValue: 'priority' },
      attachTo: document.body,
      global: { plugins },
    })
    unmount = () => wrapper.unmount()
    await flushPromises()
    expect(dialog()?.textContent).toContain('Priority of Work')
    const input = dialog()?.querySelector('input') as HTMLInputElement
    input.value = '9'
    input.dispatchEvent(new Event('input'))
    input.dispatchEvent(new Event('blur'))
    await flushPromises()
    button(dialog() as HTMLElement, 'Save priority').click()
    await flushPromises()
    expect(api.update).toHaveBeenCalledWith(account.id, { priority: 9 })
  })
})
