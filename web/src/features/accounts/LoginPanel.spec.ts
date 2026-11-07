import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { LoginSession } from '@/api/types'
import { setupApp } from '@/test/app'
import { makeAccount } from '@/test/fixtures'

import LoginPanel from './LoginPanel.vue'

type LoginState = LoginSession['state']

/**
 * Every state of the generated LoginState union. `satisfies` makes this exhaustive: a state the daemon adds to
 * the union fails the typecheck until it is listed here, and then the test below fails until it has a label.
 */
const STATES = Object.keys({
  starting: true,
  open_url: true,
  in_terminal: true,
  waiting_code: true,
  done: true,
  failed: true,
} satisfies Record<LoginState, true>) as LoginState[]

async function render(url: string, state: LoginState = 'open_url', message?: string) {
  const { plugins } = await setupApp()
  const session: LoginSession = { accountId: 'a1', state, url, ...(message ? { message } : {}) }
  return mount(LoginPanel, { props: { account: makeAccount(), session }, global: { plugins } })
}

afterEach(() => vi.restoreAllMocks())

describe('LoginPanel', () => {
  it('opens an https sign-in link in a new tab', async () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const wrapper = await render('https://accounts.example.test/sign-in?x=1')
    const button = wrapper.findAll('button').find((b) => b.text() === 'Open sign-in page')
    await button?.trigger('click')
    expect(open).toHaveBeenCalledWith(
      'https://accounts.example.test/sign-in?x=1',
      '_blank',
      'noopener,noreferrer',
    )
  })

  it.each(['javascript:alert(1)', 'file:///C:/x', 'not a url'])(
    'neither opens nor copies the link %s',
    async (url) => {
      const wrapper = await render(url)
      const labels = wrapper.findAll('button').map((b) => b.text())
      expect(labels).not.toContain('Open sign-in page')
      expect(labels).not.toContain('Copy link')
    },
  )

  // Review N1: in_terminal had no label, so the panel showed the raw key accounts.login.state.in_terminal.
  it.each(STATES)(
    'labels the %s state and shows a code box only for waiting_code',
    async (state) => {
      const wrapper = await render('https://accounts.example.test/sign-in', state)
      expect(wrapper.text()).not.toContain('accounts.login.')
      expect(wrapper.find('h4').text()).not.toBe('')
      expect(wrapper.find('input').exists()).toBe(state === 'waiting_code')
    },
  )

  it('says to finish in the terminal, without a code box or the English daemon message', async () => {
    const wrapper = await render(
      '',
      'in_terminal',
      'Finish signing in in the terminal window that opened.',
    )
    expect(wrapper.find('h4').text()).toBe('Finish signing in in the terminal window that opened')
    expect(wrapper.text()).toContain('This updates by itself when the window is done.')
    expect(wrapper.text()).not.toContain('that opened.')
    expect(wrapper.find('input').exists()).toBe(false)
    expect(wrapper.find('form').exists()).toBe(false)
  })
})
