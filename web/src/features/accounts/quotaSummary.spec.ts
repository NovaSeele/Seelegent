import { describe, expect, it } from 'vitest'

import type { Group, QuotaWindow, TierModels } from '@/api/types'
import { makeAccount } from '@/test/fixtures'

import { groupMatchesModel, quotaSummary, windowMinutes } from './quotaSummary'

const win = (window: string, remaining: number, minutes?: number): QuotaWindow => ({
  window,
  remaining,
  ...(minutes ? { minutes } : {}),
})
const group = (name: string, windows: QuotaWindow[]): Group => ({ name, windows })
const tiers = (t: Partial<TierModels>): TierModels => ({
  fast: '',
  standard: '',
  strong: '',
  max: '',
  ...t,
})

/** An Antigravity account as agy reports it: three groups, workers only ever run Gemini Flash. */
function agy(gemini: [number, number], claudeGpt: [number, number], argon: number) {
  return makeAccount({
    backend: 'agy',
    quota: {
      source: 'active',
      groups: [
        group('Gemini Models', [win('weekly', gemini[1], 10080), win('5h', gemini[0], 300)]),
        group('Claude and GPT models', [win('weekly', claudeGpt[1]), win('5h', claudeGpt[0])]),
        group('Gemini 4 Argon', [win('weekly', argon)]),
      ],
    },
  })
}

const FLASH = tiers({ fast: 'gemini-3.8-flash', standard: 'gemini-3.8-flash' })

const shown = (s: ReturnType<typeof quotaSummary>) =>
  s.windows.map((w) => `${w.group}/${w.window.window}=${w.window.remaining}`)

describe('groupMatchesModel', () => {
  it.each([
    ['Gemini Models', 'gemini-3.8-flash', true],
    ['Gemini Models', 'gemini-3.8-pro', true],
    ['Gemini 4 Argon', 'gemini-3.8-flash', false],
    ['Gemini 4 Argon', 'gemini-4-argon', true],
    ['Claude and GPT models', 'gemini-3.8-flash', false],
    ['Claude and GPT models', 'claude-sonnet-5', true],
    ['Claude and GPT models', 'gpt-oss-120b', true],
    ['Claude', 'claude-sonnet-5', true],
    ['Claude Opus', 'claude-sonnet-5', false],
    ['Claude Opus', 'claude-opus-5', true],
    ['Codex', 'gpt-5.5-codex', true],
    ['gemini-flash', 'gemini-3.8-flash', true],
  ])('%s governs %s: %s', (name, model, want) => {
    expect(groupMatchesModel(name, model)).toBe(want)
  })
})

describe('windowMinutes', () => {
  it.each([
    [win('5h', 1), 300],
    [win('weekly', 1), 10080],
    [win('7d', 1), 10080],
    [win('30m', 1), 30],
    [win('window', 1), 0],
    [win('window', 1, 600), 600],
  ])('%o lasts %i minutes', (w, want) => {
    expect(windowMinutes(w)).toBe(want)
  })
})

describe('quotaSummary', () => {
  it('shows the 5h and weekly windows of the group a tier model belongs to', () => {
    const s = quotaSummary(agy([0.93, 0.97], [1, 1], 1), FLASH)
    expect(s.routing).toBe(true)
    expect(shown(s)).toEqual(['Gemini Models/5h=0.93', 'Gemini Models/weekly=0.97'])
    expect(s.more).toBe(3)
  })

  it('ignores Argon and Claude-and-GPT groups drained to 0% (workers never use them)', () => {
    const s = quotaSummary(agy([0.62, 0.8], [0, 0], 0), FLASH)
    expect(s.routing).toBe(true)
    expect(shown(s)).toEqual(['Gemini Models/5h=0.62', 'Gemini Models/weekly=0.8'])
  })

  it('falls back to the lowest window of all when no group matches a tier model', () => {
    const s = quotaSummary(agy([0.62, 0.8], [0.4, 0.1], 0.5), tiers({}))
    expect(s.routing).toBe(false)
    expect(shown(s)).toEqual(['Claude and GPT models/weekly=0.1'])
    expect(s.more).toBe(4)
  })

  it('falls back without settings (not read yet)', () => {
    const s = quotaSummary(agy([0.62, 0.8], [0.4, 0.1], 0.5), undefined)
    expect(s.routing).toBe(false)
    expect(shown(s)).toEqual(['Claude and GPT models/weekly=0.1'])
  })

  it('takes the lowest window of each length across the matching groups (Claude sub-limits)', () => {
    const account = makeAccount({
      backend: 'claude',
      quota: {
        source: 'active',
        groups: [
          group('Claude', [win('5h', 0.7), win('weekly', 0.9)]),
          group('Claude Opus', [win('weekly', 0.3)]),
          group('Claude Sonnet', [win('weekly', 0.05)]),
        ],
      },
    })
    const s = quotaSummary(account, tiers({ strong: 'claude-opus-5' }))
    expect(shown(s)).toEqual(['Claude/5h=0.7', 'Claude Opus/weekly=0.3'])
    expect(s.more).toBe(2)
  })

  it('fills two bars from the routing group when it has no 5h / weekly pair, and has no +N then', () => {
    const account = makeAccount({
      backend: 'codex',
      quota: {
        source: 'active',
        groups: [group('Codex', [win('window', 0.5), win('30m', 0.9)])],
      },
    })
    const s = quotaSummary(account, tiers({ standard: 'gpt-5.5-codex' }))
    expect(shown(s)).toEqual(['Codex/window=0.5', 'Codex/30m=0.9'])
    expect(s.more).toBe(0)
  })

  it('is empty without quota groups', () => {
    expect(quotaSummary(makeAccount(), FLASH)).toEqual({ windows: [], routing: false, more: 0 })
  })
})
