import { describe, expect, it } from 'vitest'

import { parseModels, providerInput } from './providerForm'

const form = {
  id: 'zai',
  name: 'Z.ai',
  backend: 'claude' as const,
  baseUrl: 'https://api.z.ai/api/anthropic',
  enabled: true,
  models: [],
}

describe('provider form', () => {
  it('splits models on commas and spaces', () => {
    expect(parseModels(' glm-5, glm-5-air  kimi ,, ')).toEqual(['glm-5', 'glm-5-air', 'kimi'])
  })

  it('sends the preset a provider was made from, and none for a blank one', () => {
    expect(providerInput(form, ['glm-5'], 'zai')).toEqual({
      ...form,
      models: ['glm-5'],
      preset: 'zai',
    })
    expect(providerInput(form, [], '')).not.toHaveProperty('preset')
  })
})
