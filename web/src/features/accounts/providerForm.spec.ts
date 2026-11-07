import { describe, expect, it } from 'vitest'

import { makeProvider } from '@/test/fixtures'

import { parseModels, providerChanges, providerInput } from './providerForm'

const form = {
  id: 'zai',
  name: 'Z.ai',
  backend: 'claude' as const,
  baseUrl: 'https://api.z.ai/api/anthropic',
  enabled: true,
  models: [],
  official: false,
}

describe('provider form', () => {
  it('splits models on commas and spaces', () => {
    expect(parseModels(' glm-5, glm-5-air  kimi ,, ')).toEqual(['glm-5', 'glm-5-air', 'kimi'])
  })

  it('sends the preset a provider was made from, and none for a blank one', () => {
    expect(providerInput(form, ['glm-5'], 'zai')).toEqual({
      id: 'zai',
      name: 'Z.ai',
      backend: 'claude',
      baseUrl: 'https://api.z.ai/api/anthropic',
      enabled: true,
      models: ['glm-5'],
      preset: 'zai',
    })
    expect(providerInput(form, [], '')).not.toHaveProperty('preset')
  })

  it('never sends the read-only official flag (ProviderInput has no such field; the daemon refuses unknown ones)', () => {
    expect(providerInput({ ...form, official: true }, [], 'anthropic')).not.toHaveProperty(
      'official',
    )
  })

  // Live run: the daemon answers models [] for a provider saved with a model list (the list lives in
  // allowedModels), so re-sending the untouched field on an edit wiped the allow-list.
  it('sends only the changed fields on an edit, and models only when they changed', () => {
    const before = makeProvider({ name: 'Z.ai', models: [], allowedModels: '^(?:glm-5)$' })
    const same = { ...form, name: 'Z.ai' }
    expect(providerChanges(before, same, [])).toEqual({})
    expect(providerChanges(before, { ...same, name: 'GLM', enabled: false }, [])).toEqual({
      name: 'GLM',
      enabled: false,
    })
    expect(providerChanges(before, same, ['glm-5', 'glm-5-air'])).toEqual({
      models: ['glm-5', 'glm-5-air'],
    })
    const official = makeProvider({ id: 'anthropic', baseUrl: '', official: true })
    expect(providerChanges(official, { ...form, ...official, baseUrl: 'https://x' }, [])).toEqual(
      {},
    )
  })
})
