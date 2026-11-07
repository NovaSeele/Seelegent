import { describe, expect, it } from 'vitest'

import { applyAllowList } from './allow-list.mjs'

const listed = () => ({ models: ['a', 'b'], allowedModels: '^(?:a|b)$' })
const anyModel = () => ({ models: [], allowedModels: '' })
const pattern = () => ({ models: [], allowedModels: '^glm-' })

/** The provider after applying a body to it, as the daemon's applyProvider does. */
function apply(p, body) {
  applyAllowList(p, body.models, body.allowedModels)
  return p
}

// Review G4: the mock must update the allow-list in the daemon's order (internal/httpapi applyProvider).
describe('mock applyAllowList', () => {
  it('lets a non-empty models win and ignores allowedModels then', () => {
    expect(apply(pattern(), { models: [' c ', 'c', '', 'd'], allowedModels: '^x' })).toEqual({
      models: ['c', 'd'],
      allowedModels: '^(?:c|d)$',
    })
  })

  it('sets a pattern from allowedModels, reading a list-shaped one back as its list', () => {
    expect(apply(listed(), { allowedModels: ' ^x ' })).toEqual({ models: [], allowedModels: '^x' })
    expect(apply(anyModel(), { allowedModels: '^(?:c|d)$' })).toEqual({
      models: ['c', 'd'],
      allowedModels: '^(?:c|d)$',
    })
  })

  it('sets the pattern for models [] with allowedModels when no list is stored', () => {
    expect(apply(anyModel(), { models: [], allowedModels: '^x' })).toEqual({
      models: [],
      allowedModels: '^x',
    })
    expect(apply(pattern(), { models: [], allowedModels: '^x' })).toEqual({
      models: [],
      allowedModels: '^x',
    })
  })

  it("clears both when models [] comes with the stored list's own pattern (its echo)", () => {
    expect(apply(listed(), { models: [], allowedModels: '^(?:a|b)$' })).toEqual(anyModel())
    // Another pattern is no echo: it is set.
    expect(apply(listed(), { models: [], allowedModels: '^x' })).toEqual({
      models: [],
      allowedModels: '^x',
    })
  })

  it('clears both for models [] alone, and leaves the allow-list without either field', () => {
    expect(apply(listed(), { models: [] })).toEqual(anyModel())
    expect(apply(pattern(), { models: ['', ' '] })).toEqual(anyModel())
    expect(apply(pattern(), {})).toEqual(pattern())
  })

  it('refuses a model id with a space and a pattern that does not compile', () => {
    expect(() => apply(listed(), { models: ['a b'] })).toThrow(/spaces/)
    expect(() => apply(listed(), { allowedModels: '(' })).toThrow(/regular expression/)
  })
})
