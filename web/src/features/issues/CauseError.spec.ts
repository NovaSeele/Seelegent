import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import { i18n } from '@/i18n'
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

  // Design review: a failed repo sync said only "GitHub returned an error". Every cause names a next step, in
  // every locale, and a repository GitHub cannot find has its own sentence.
  it('gives every cause a next step in every locale', async () => {
    const causes = [
      'git_auth',
      'git_rejected',
      'git',
      'network',
      'github_auth',
      'github_rate_limited',
      'github_forbidden',
      'github_not_found',
      'github_manual',
      'github',
      'other',
    ]
    for (const locale of ['en', 'es', 'ja', 'ko', 'ru', 'vi', 'zh-CN'] as const) {
      await setupApp(locale)
      const sentences = causes.map((c) => i18n.global.t(causeKey(c)))
      for (const [i, s] of sentences.entries()) {
        expect(i18n.global.te(causeKey(causes[i])), `${locale} ${causes[i]}`).toBe(true)
        // Two sentences at least: what went wrong, then what to do (a full stop of any script between them).
        expect(
          s.split(/[.。]\s*/).filter(Boolean).length,
          `${locale} ${causes[i]}: ${s}`,
        ).toBeGreaterThan(1)
      }
      expect(new Set(sentences).size).toBe(causes.length)
    }
    expect(causeKey('github_not_found')).toBe('issues.cause.githubNotFound')
  })

  it('reads an unknown or missing cause as other', () => {
    expect(causeKey(undefined)).toBe('issues.cause.other')
    expect(causeKey('surprise')).toBe('issues.cause.other')
    expect(causeKey('github_rate_limited')).toBe('issues.cause.githubRateLimited')
  })
})
