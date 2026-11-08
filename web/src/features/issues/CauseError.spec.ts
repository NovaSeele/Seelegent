import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import { setupApp } from '@/test/app'

import CauseError from './CauseError.vue'
import { causeKey } from './causeText'

describe('CauseError (F14)', () => {
  it('says what went wrong from the cause and keeps git words behind the disclosure', async () => {
    const { plugins } = await setupApp()
    const wrapper = mount(CauseError, {
      props: { cause: 'git_auth', text: 'fatal: Authentication failed for https://github.com/o/r' },
      global: { plugins },
    })
    expect(wrapper.find('.cause__why').text()).toContain('Git could not sign in to the remote')
    const said = wrapper.find('details')
    expect(said.find('summary').text()).toBe('Seelegent said:')
    expect(said.find('code').text()).toContain('Authentication failed')
  })

  it('translates the sentence', async () => {
    const { plugins } = await setupApp('ja')
    const wrapper = mount(CauseError, {
      props: { cause: 'network', text: 'dial tcp: i/o timeout' },
      global: { plugins },
    })
    expect(wrapper.find('.cause__why').text()).not.toContain('could not be reached')
    expect(wrapper.find('summary').text()).toBe('Seelegent のメッセージ：')
  })

  it('reads an unknown or missing cause as other', () => {
    expect(causeKey(undefined)).toBe('issues.cause.other')
    expect(causeKey('surprise')).toBe('issues.cause.other')
    expect(causeKey('github_rate_limited')).toBe('issues.cause.githubRateLimited')
  })
})
