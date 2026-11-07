import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { LoginSession } from '@/api/types'
import { setupApp } from '@/test/app'
import { makeAccount } from '@/test/fixtures'

import LoginPanel from './LoginPanel.vue'

async function render(url: string) {
  const { plugins } = await setupApp()
  const session: LoginSession = { accountId: 'a1', state: 'open_url', url }
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
})
