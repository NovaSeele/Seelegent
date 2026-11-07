import { describe, expect, it } from 'vitest'

import { makeProvider } from '@/test/fixtures'

import {
  allowList,
  exactPattern,
  parseModels,
  providerChanges,
  providerInput,
  tierModels,
} from './providerForm'

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

  it('builds the exact pattern like the daemon (regexp.QuoteMeta per model)', () => {
    expect(exactPattern([])).toBe('')
    expect(exactPattern(['gpt-5.5', 'o+1', 'a|b'])).toBe('^(?:gpt-5\\.5|o\\+1|a\\|b)$')
  })

  // Contract: models is the allow-list as last set, allowedModels the enforced pattern; a hand-written pattern
  // comes with models []; both empty is any model; tier models are allowed on top and are not in the list.
  describe('allow-list', () => {
    const p = (allowedModels: string, models: string[] = []) => ({ models, allowedModels })

    it('takes the list from models when it agrees with the pattern', () => {
      expect(allowList(p('^(?:deepseek-v4|deepseek-r2)$', ['deepseek-v4', 'deepseek-r2']))).toEqual(
        { kind: 'list', models: ['deepseek-v4', 'deepseek-r2'] },
      )
      expect(allowList(p('^(?:gpt-5\\.5|o\\+1|a\\|b)$'))).toEqual({
        kind: 'list',
        models: ['gpt-5.5', 'o+1', 'a|b'],
      })
    })

    it('treats both empty as any model', () => {
      expect(allowList(p(''))).toEqual({ kind: 'any' })
    })

    it('keeps a hand-written pattern as a pattern', () => {
      expect(allowList(p('^glm-'))).toEqual({ kind: 'pattern', pattern: '^glm-' })
      expect(allowList(p('^(?:glm-.*)$'))).toEqual({ kind: 'pattern', pattern: '^(?:glm-.*)$' })
    })

    // The daemon's ModelsOf: a list-shaped pattern counts as a list only on an exact round trip, and a part with
    // a space or a control character is no model id, so the pattern is hand-written (models []).
    it.each([
      '^(?:a b|c)$',
      '^(?:a\tb|c)$',
      '^(?:c|a\u0001)$',
      '^(?:a\\-b)$',
      '^(?:a||b)$',
      '^(?:a\\)$',
      '^(?:)$',
    ])('reads %s as a hand-written pattern, as the daemon does', (pattern) => {
      expect(allowList(p(pattern))).toEqual({ kind: 'pattern', pattern })
    })

    // Review S1: a daemon that answers models with the tier models must not put a tier model in the field.
    it('never takes tier models answered in models as the list', () => {
      const tiered = p('^(?:deepseek-v4|deepseek-r2)$', ['deepseek-chat'])
      expect(allowList(tiered)).toEqual({ kind: 'list', models: ['deepseek-v4', 'deepseek-r2'] })
      expect(allowList(p('^glm-', ['glm-5-air']))).toEqual({ kind: 'pattern', pattern: '^glm-' })
      expect(allowList(p('', ['glm-5-air']))).toEqual({ kind: 'any' })
    })
  })

  it('lists the tier models once each, in tier order', () => {
    const tiers = { fast: 'deepseek-chat', standard: 'deepseek-v4', strong: 'deepseek-v4', max: '' }
    expect(tierModels({ tiers })).toEqual(['deepseek-chat', 'deepseek-v4'])
  })

  it('round-trips a list with tier models without the tier model reaching the body', () => {
    const before = makeProvider({
      id: 'deepseek',
      name: 'DeepSeek',
      models: ['deepseek-chat'],
      allowedModels: '^(?:deepseek-v4|deepseek-r2)$',
      tiers: { fast: 'deepseek-chat', standard: '', strong: '', max: '' },
    })
    const same = { ...form, name: 'DeepSeek' }
    expect(providerChanges(before, same, ['deepseek-v4', 'deepseek-r2'])).toEqual({})
    expect(providerChanges(before, same, ['deepseek-v4', 'deepseek-r2', 'deepseek-v5'])).toEqual({
      models: ['deepseek-v4', 'deepseek-r2', 'deepseek-v5'],
    })
  })

  // Live run: re-sending the untouched models field on an edit replaced the allow-list.
  it('sends only the changed fields on an edit, and models only when they changed', () => {
    // A hand-written pattern: the field is empty, and leaving it so must not replace the pattern.
    const before = makeProvider({ name: 'Z.ai', models: [], allowedModels: '^glm-' })
    const same = { ...form, name: 'Z.ai' }
    expect(providerChanges(before, same, [])).toEqual({})
    expect(providerChanges(before, { ...same, name: 'GLM', enabled: false }, [])).toEqual({
      name: 'GLM',
      enabled: false,
    })
    // A list typed over the pattern replaces it (the dialog says so).
    expect(providerChanges(before, same, ['glm-5', 'glm-5-air'])).toEqual({
      models: ['glm-5', 'glm-5-air'],
    })
    // Any model: an empty field is no change.
    expect(providerChanges(makeProvider({ name: 'Z.ai' }), same, [])).toEqual({})
    // A list the user cleared is a real change: [] clears it to any model.
    const listed = makeProvider({ name: 'Z.ai', models: ['glm-5'], allowedModels: '^(?:glm-5)$' })
    expect(providerChanges(listed, same, ['glm-5'])).toEqual({})
    expect(providerChanges(listed, same, [])).toEqual({ models: [] })
    const official = makeProvider({ id: 'anthropic', baseUrl: '', official: true })
    expect(providerChanges(official, { ...form, ...official, baseUrl: 'https://x' }, [])).toEqual(
      {},
    )
  })

  // Review G3: an empty field means "unchanged", so a hand-written pattern could never go back to any model.
  it('sends models [] for an empty field when the user allows any model', () => {
    const before = makeProvider({ name: 'Z.ai', models: [], allowedModels: '^glm-' })
    const same = { ...form, name: 'Z.ai' }
    expect(providerChanges(before, same, [], true)).toEqual({ models: [] })
    // A typed list still wins over the button.
    expect(providerChanges(before, same, ['glm-5'], true)).toEqual({ models: ['glm-5'] })
  })
})
