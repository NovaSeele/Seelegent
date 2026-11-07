import { describe, expect, it } from 'vitest'

import type { Member } from '@/api/types'

import { MEMBER_ID, draftOf, inputOf, sortMembers, trackFacts } from './memberModel'

const member = (overrides: Partial<Member> = {}): Member => ({
  id: 'rev',
  name: 'Reviewer',
  backend: 'claude',
  rank: 'peer',
  preferredAccounts: [],
  fallbackBackends: ['agy', 'opencode'],
  enabled: true,
  ...overrides,
})

describe('memberModel', () => {
  it('drafts a new member as an enabled worker on agy', () => {
    expect(draftOf()).toEqual({
      id: '',
      name: '',
      rank: 'worker',
      backend: 'agy',
      provider: '',
      tier: '',
      model: '',
      role: '',
      fallbackBackends: [],
      enabled: true,
    })
  })

  it('keeps only known fallback backends when editing', () => {
    expect(draftOf(member()).fallbackBackends).toEqual(['agy'])
  })

  it('sends every field so a cleared one clears, without its own backend as a fallback', () => {
    const d = { ...draftOf(member({ model: 'x', tier: 'strong' })), model: '  ', tier: '' as const }
    d.fallbackBackends = ['claude', 'agy']
    expect(inputOf(d)).toEqual({
      id: 'rev',
      name: 'Reviewer',
      rank: 'peer',
      backend: 'claude',
      provider: '',
      tier: '',
      model: '',
      role: '',
      fallbackBackends: ['agy'],
      enabled: true,
    })
  })

  it("runs a provider member on the provider's backend with no fallbacks", () => {
    const d = { ...draftOf(member()), id: ' ', provider: 'zai', fallbackBackends: ['agy' as const] }
    const input = inputOf(d, 'claude')
    expect(input).not.toHaveProperty('id')
    expect(input).toMatchObject({ provider: 'zai', backend: 'claude', fallbackBackends: [] })
  })

  it('lists enabled members first, then by name', () => {
    const list = [
      member({ id: 'c', name: 'Cleo', enabled: false }),
      member({ id: 'b', name: 'Bao' }),
      member({ id: 'a', name: 'An', enabled: false }),
      member({ id: 'd', name: 'Duy' }),
    ]
    expect(sortMembers(list).map((m) => m.id)).toEqual(['b', 'd', 'a', 'c'])
  })

  it('shows only the non-zero numbers of a track record', () => {
    expect(trackFacts(undefined)).toEqual([])
    expect(
      trackFacts({
        member: 'rev',
        meetings: 3,
        dissents: 0,
        concessions: 1,
        signals: 0,
        vindicated: 1,
        dismissed: 0,
        adviceNotTaken: 0,
        consults: 2,
        objections: 0,
      }),
    ).toEqual([
      { key: 'meetings', value: 3 },
      { key: 'vindicated', value: 1 },
      { key: 'consults', value: 2 },
    ])
  })

  it('accepts ids the daemon accepts', () => {
    expect(MEMBER_ID.test('rev-2')).toBe(true)
    expect(MEMBER_ID.test('-rev')).toBe(false)
    expect(MEMBER_ID.test('Rev')).toBe(false)
    expect(MEMBER_ID.test('a'.repeat(41))).toBe(false)
  })
})
