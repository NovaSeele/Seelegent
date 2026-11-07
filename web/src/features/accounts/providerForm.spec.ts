import { describe, expect, it } from 'vitest'

import { makeProvider } from '@/test/fixtures'

import { parseModels, providerChanges, providerInput, providerModels } from './providerForm'

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

  // Live run: the daemon keeps a saved model list only as an exact allowedModels pattern.
  it('reads the model list back from an exact allowedModels pattern', () => {
    const p = (allowedModels: string, models: string[] = []) => ({ models, allowedModels })
    expect(providerModels(p('', ['a']))).toEqual(['a'])
    expect(providerModels(p(''))).toEqual([])
    expect(providerModels(p('^(?:claude-sonnet-5|claude-opus-5)$'))).toEqual([
      'claude-sonnet-5',
      'claude-opus-5',
    ])
    expect(providerModels(p('^(?:gpt-5\\.5|o\\+1|a\\|b)$'))).toEqual(['gpt-5.5', 'o+1', 'a|b'])
    expect(providerModels(p('^gemini-3\\.8-'))).toBeNull()
    expect(providerModels(p('^(?:glm-.*)$'))).toBeNull()
  })

  it('compares an edit with the list read back from the pattern', () => {
    const before = makeProvider({ name: 'Z.ai', models: [], allowedModels: '^(?:glm-5)$' })
    expect(providerChanges(before, { ...form, name: 'Z.ai' }, ['glm-5'])).toEqual({})
  })

  // Live run: the daemon answers models [] for a provider saved with a model list (the list lives in
  // allowedModels), so re-sending the untouched field on an edit wiped the allow-list.
  it('sends only the changed fields on an edit, and models only when they changed', () => {
    // A hand-written pattern: the form shows no models, and an untouched field must not replace the pattern.
    const before = makeProvider({ name: 'Z.ai', models: [], allowedModels: '^glm-' })
    const same = { ...form, name: 'Z.ai' }
    expect(providerChanges(before, same, [])).toEqual({})
    expect(providerChanges(before, { ...same, name: 'GLM', enabled: false }, [])).toEqual({
      name: 'GLM',
      enabled: false,
    })
    expect(providerChanges(before, same, ['glm-5', 'glm-5-air'])).toEqual({
      models: ['glm-5', 'glm-5-air'],
    })
    // An exact list the user cleared is a real change.
    const listed = makeProvider({ name: 'Z.ai', allowedModels: '^(?:glm-5)$' })
    expect(providerChanges(listed, same, [])).toEqual({ models: [] })
    const official = makeProvider({ id: 'anthropic', baseUrl: '', official: true })
    expect(providerChanges(official, { ...form, ...official, baseUrl: 'https://x' }, [])).toEqual(
      {},
    )
  })
})
