import { describe, expect, it } from 'vitest'

import { makeProvider } from '@/test/fixtures'

import {
  checkKey,
  detectPreset,
  MAX_KEY_BYTES,
  orderedPresets,
  parseService,
  serviceForPreset,
  serviceValue,
} from './apiKey'

// Characters spelled out, so no escape in this file can be lost on the way.
const ch = (code: number) => String.fromCharCode(code)
const TAB = ch(9)
const LF = ch(10)
const CR = ch(13)
const NUL = ch(0)
const NBSP = ch(0xa0)
const IDEOGRAPHIC_SPACE = ch(0x3000)

const SECRET = 'sk-ant-api03-SECRETSECRET'

// The cases of the daemon's TestNormalizeKey (internal/account/apikey_test.go): the form must clean and refuse
// a key exactly like the server does, so nothing it lets through comes back as a field error.
describe('checkKey', () => {
  it('cleans what the daemon cleans, and says what it did', () => {
    const cases: [string, { removedName?: string; quotesRemoved?: boolean }][] = [
      [SECRET, {}],
      [`  ${SECRET}${LF}`, {}],
      [`ANTHROPIC_API_KEY=${SECRET}`, { removedName: 'ANTHROPIC_API_KEY' }],
      [`export LLM_API_KEY = "${SECRET}"`, { removedName: 'LLM_API_KEY', quotesRemoved: true }],
      [`'${SECRET}'`, { quotesRemoved: true }],
      [`\`${SECRET}\``, { quotesRemoved: true }],
      [`${NBSP}${SECRET}${IDEOGRAPHIC_SPACE}`, {}],
    ]
    for (const [raw, want] of cases) {
      const c = checkKey(raw)
      expect(c.key, raw).toBe(SECRET)
      expect(c.error, raw).toBeUndefined()
      expect(c.removedName, raw).toBe(want.removedName ?? '')
      expect(c.quotesRemoved, raw).toBe(want.quotesRemoved ?? false)
      expect(c.assignment, raw).toBe(false)
    }
    expect(checkKey('export OPENAI_API_KEY="sk-proj-0123456789"').key).toBe('sk-proj-0123456789')
  })

  it('refuses what the daemon refuses, in the same order', () => {
    const cases: [string, string][] = [
      ['', 'empty'],
      ['   ', 'empty'],
      ['k'.repeat(MAX_KEY_BYTES + 1), 'too_long'],
      ['é'.repeat(MAX_KEY_BYTES / 2 + 1), 'too_long'],
      ['https://api.anthropic.com/v1/', 'url'],
      ['sk-ant-abc://def', 'url'],
      ['sk-ant-abc def', 'whitespace'],
      [`sk-ant-abc${LF}def`, 'whitespace'],
      [`sk-ant-abc${CR}${LF}def`, 'whitespace'],
      [`sk-ant-abc${TAB}def`, 'whitespace'],
      [`sk-ant-abc${NBSP}def`, 'whitespace'],
      [`sk-ant-abc${NUL}def`, 'control'],
      ['https://x y', 'url'],
    ]
    for (const [raw, reason] of cases) expect(checkKey(raw).error, JSON.stringify(raw)).toBe(reason)
  })

  it('keeps keys that hold "=" whole; a lower-case name=value only gets advice', () => {
    for (const raw of [
      'QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVpkZWZnaGlqa2xtbm9wZGVm==',
      'ABCDEFGH==ijklmnopqrst',
      'KEY=short',
      'llm_key=short12',
    ]) {
      const c = checkKey(raw)
      expect(c).toEqual({ key: raw, removedName: '', quotesRemoved: false, assignment: false })
    }
    for (const raw of ['llm_api_key=sk-0123456789abcdef', 'abc_DEF=xyz12345', `Key1=${SECRET}`]) {
      const c = checkKey(raw)
      expect(c.key).toBe(raw)
      expect(c.assignment, raw).toBe(true)
    }
    const quoted = checkKey('"llm_api_key=sk-0123456789abcdef"')
    expect(quoted.quotesRemoved && quoted.assignment).toBe(true)
  })

  it('strips a long variable name too', () => {
    const long = 'A'.repeat(41)
    const c = checkKey(`${long}=${SECRET}`)
    expect(c.key).toBe(SECRET)
    expect(c.removedName).toBe(long)
  })
})

describe('detectPreset', () => {
  it('names only the services whose prefix is theirs alone', () => {
    expect(detectPreset('sk-ant-api03-x')).toBe('anthropic')
    expect(detectPreset('sk-or-v1-x')).toBe('openrouter')
    expect(detectPreset('gsk_abc')).toBe('groq')
    expect(detectPreset('sk-proj-abc')).toBeNull()
    expect(detectPreset('sk-abc')).toBeNull()
  })
})

describe('services', () => {
  it('round-trips the form values', () => {
    expect(parseService(serviceValue({ kind: 'provider', id: 'my:proxy' }))).toEqual({
      kind: 'provider',
      id: 'my:proxy',
    })
    expect(parseService('preset:deepseek')).toEqual({ kind: 'preset', id: 'deepseek' })
    expect(parseService('')).toBeNull()
    expect(parseService('preset:')).toBeNull()
    expect(parseService('other:x')).toBeNull()
  })

  it('sends a detected key to the user’s own enabled service of that preset, else to the preset', () => {
    const own = makeProvider({ id: 'anthropic', preset: 'anthropic' })
    const off = makeProvider({ id: 'anthropic-2', preset: 'anthropic', enabled: false })
    expect(serviceForPreset('anthropic', [off, own])).toEqual({ kind: 'provider', id: 'anthropic' })
    expect(serviceForPreset('anthropic', [off])).toEqual({ kind: 'preset', id: 'anthropic' })
    expect(serviceForPreset('groq', [own])).toEqual({ kind: 'preset', id: 'groq' })
  })

  it('offers the well-known services first and the two "Other" ones last', () => {
    const p = (id: string) => ({
      id,
      name: id,
      backend: 'claude' as const,
      baseUrl: '',
      enabled: true,
      models: [],
      official: false,
    })
    const ids = orderedPresets([p('custom-openai'), p('zzz'), p('groq'), p('anthropic')]).map(
      (x) => x.id,
    )
    expect(ids).toEqual(['anthropic', 'groq', 'custom-openai', 'zzz'])
  })
})
