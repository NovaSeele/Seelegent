import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import { setupApp } from '@/test/app'

import { providerEdge, providerToken } from './provider'
import ProviderMark from './ProviderMark.vue'

async function mark(props: InstanceType<typeof ProviderMark>['$props']) {
  const { plugins } = await setupApp()
  return mount(ProviderMark, { props, global: { plugins } })
}

describe('ProviderMark', () => {
  it.each([
    ['agy', 'Antigravity', 'var(--provider-agy)'],
    ['claude', 'Claude Code', 'var(--provider-claude)'],
    ['codex', 'Codex', 'var(--provider-codex)'],
  ])('%s: its hue on the dot and its name beside it', async (backend, name, hue) => {
    const wrapper = await mark({ backend })
    expect(wrapper.find('.pm__name').text()).toBe(name)
    expect(wrapper.attributes('style')).toContain(`--pm-hue: ${hue}`)
    expect(wrapper.find('.pm__dot').attributes('aria-hidden')).toBe('true')
  })

  it('gives an unknown backend the neutral hue and its id as the name', async () => {
    const wrapper = await mark({ backend: 'gemini-cli' })
    expect(wrapper.find('.pm__name').text()).toBe('gemini-cli')
    expect(wrapper.attributes('style')).toContain('--pm-hue: var(--provider-other)')
  })

  it('shows an API-key provider as a neutral chip next to the backend', async () => {
    const wrapper = await mark({ backend: 'claude', provider: 'DeepSeek' })
    expect(wrapper.find('.pm__name').text()).toBe('Claude Code')
    expect(wrapper.find('.pm__chip').text()).toBe('DeepSeek')
    // The provider has no hue of its own: the mark keeps the backend's.
    expect(wrapper.attributes('style')).toContain('--pm-hue: var(--provider-claude)')
  })

  it('leaves the name out with label false, and sizes', async () => {
    const wrapper = await mark({ backend: 'agy', label: false, size: 'sm' })
    expect(wrapper.find('.pm__name').exists()).toBe(false)
    expect(wrapper.classes()).toContain('pm--sm')
  })
})

describe('provider tokens', () => {
  it('names the hue, its steps and the edge style', () => {
    expect(providerToken('agy', 'soft')).toBe('var(--provider-agy-soft)')
    expect(providerToken(undefined, 'border')).toBe('var(--provider-other-border)')
    expect(providerEdge('codex')).toEqual({ '--provider-edge': 'var(--provider-codex)' })
    expect(providerEdge('nope')).toEqual({ '--provider-edge': 'var(--provider-other)' })
  })
})
