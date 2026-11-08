import { mount, RouterLinkStub } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import type { Account, Settings } from '@/api/types'
import ProgressBar from '@/components/ui/ProgressBar.vue'
import QuotaBar from '@/components/ui/QuotaBar.vue'
import { useAccountsStore } from '@/stores/accounts'
import { useConnectionStore } from '@/stores/connection'
import { setupApp } from '@/test/app'
import { makeAccount } from '@/test/fixtures'

import AccountStrip from './AccountStrip.vue'

/** An Antigravity account with room on Gemini and its Argon and Claude-and-GPT groups drained. */
function agy(): Account {
  return makeAccount({
    backend: 'agy',
    quota: {
      source: 'active',
      groups: [
        {
          name: 'Gemini Models',
          windows: [
            { window: '5h', remaining: 0.62 },
            { window: 'weekly', remaining: 0.4 },
          ],
        },
        { name: 'Claude and GPT models', windows: [{ window: '5h', remaining: 0 }] },
        { name: 'Gemini 4 Argon', windows: [{ window: 'weekly', remaining: 0 }] },
      ],
    },
  })
}

async function render(flash: string) {
  const { plugins } = await setupApp()
  const tier = { enabled: true, allowedModels: '' }
  const none = { fast: '', standard: '', strong: '', max: '' }
  useConnectionStore().settings = {
    backends: {
      agy: { ...tier, tiers: { ...none, fast: flash, standard: flash } },
      claude: { ...tier, tiers: none },
      codex: { ...tier, tiers: none, sandbox: 'workspace-write' },
    },
  } as unknown as Settings
  useAccountsStore().replaceAll([agy()])
  return mount(AccountStrip, {
    props: { now: Date.now() },
    global: { plugins, stubs: { RouterLink: RouterLinkStub } },
  })
}

describe('AccountStrip', () => {
  it('shows the tightest window of the routing group, not a drained group workers never use', async () => {
    const wrapper = await render('gemini-3.8-flash')
    const bar = wrapper.getComponent(QuotaBar)
    expect(bar.props('label')).toBe('Gemini Models · weekly')
    expect(bar.props('muted')).toBe(false)
    expect(wrapper.findAll('.quota__value').map((v) => v.text())).toEqual(['40% left'])
    expect(wrapper.text()).not.toContain('Argon')
    expect(wrapper.getComponent(ProgressBar).props('tone')).toBe('neutral')
  })

  it('falls back to the lowest of all windows in grey when no group matches, and says so', async () => {
    const wrapper = await render('mystery-model')
    const bar = wrapper.getComponent(QuotaBar)
    expect(bar.props('muted')).toBe(true)
    expect(wrapper.getComponent(ProgressBar).props('tone')).toBe('muted')
    expect(bar.attributes('title')).toBe('Lowest of all windows: Claude and GPT models · 5h')
  })
})
