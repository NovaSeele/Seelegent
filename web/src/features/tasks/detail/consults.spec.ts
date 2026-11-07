import { describe, expect, it } from 'vitest'

import type { Consult } from '@/api/types'

import { consultState, consultTone, isOpenObjection, memberLabel } from './consults'

const consult = (overrides: Partial<Consult>): Consult => ({
  id: 'c1',
  member: 'peri',
  rank: 'peer',
  runTaskId: 'r1',
  status: 'done',
  verdict: 'object',
  findings: [],
  required: [],
  at: '2026-11-07T10:00:00.000Z',
  answeredObjection: null,
  ...overrides,
})

describe('consults', () => {
  it('only an unanswered peer objection blocks accepting and asks for attention', () => {
    const open = consult({})
    expect(isOpenObjection(open)).toBe(true)
    expect(consultTone(open)).toBe('attention')
    expect(consultState(open)).toBe('object')

    const answered = consult({
      answeredObjection: { text: 'kept', at: '2026-11-07T11:00:00.000Z' },
    })
    expect(isOpenObjection(answered)).toBe(false)
    expect(consultTone(answered)).toBe('muted')
    expect(consultState(answered)).toBe('answered')

    const senior = consult({ rank: 'senior' })
    expect(isOpenObjection(senior)).toBe(false)
    expect(consultTone(senior)).toBe('neutral')
  })

  it('concerns are neutral, approval success, a running or failed run says so', () => {
    expect(consultTone(consult({ verdict: 'concerns' }))).toBe('neutral')
    expect(consultTone(consult({ verdict: 'approve' }))).toBe('success')
    expect(consultTone(consult({ status: 'running', verdict: undefined }))).toBe('running')
    expect(consultState(consult({ status: 'failed', verdict: undefined }))).toBe('failed')
    expect(consultTone(consult({ status: 'failed', verdict: undefined }))).toBe('danger')
  })

  it('names a member by its name, else its id', () => {
    const members = [{ id: 'peri', name: 'Peri Okafor' }] as never
    expect(memberLabel(members, 'peri')).toBe('Peri Okafor')
    expect(memberLabel(members, 'ana')).toBe('ana')
  })
})
