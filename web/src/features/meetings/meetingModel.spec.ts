import { describe, expect, it } from 'vitest'

import type { Citation, Meeting } from '@/api/types'

import {
  attendeeName,
  citeText,
  citeTone,
  contestOpen,
  lines,
  meetingOpen,
  pendingQuestions,
} from './meetingModel'

// Only the fields the helpers read; the rest of a meeting does not matter here.
const meeting = (m: Pick<Meeting, 'leadQa' | 'attendees'> & Partial<Pick<Meeting, 'status'>>) =>
  ({ status: 'waiting_lead', ...m }) as Meeting

const cite = (overrides: Partial<Citation> = {}): Citation => ({
  path: 'internal/a.go',
  line: 12,
  state: 'verified',
  ...overrides,
})

describe('meetingModel', () => {
  it('trusts only checked citations', () => {
    expect(citeTone('verified')).toBe('success')
    expect(citeTone('located')).toBe('neutral')
    for (const s of ['quote_mismatch', 'bad_range', 'missing_file', 'outside_repo'] as const) {
      expect(citeTone(s)).toBe('danger')
    }
  })

  it('writes a citation as path:line or path:line-end', () => {
    expect(citeText(cite())).toBe('internal/a.go:12')
    expect(citeText(cite({ endLine: 12 }))).toBe('internal/a.go:12')
    expect(citeText(cite({ endLine: 20 }))).toBe('internal/a.go:12-20')
  })

  it('reads one entry per non-empty line', () => {
    expect(lines(' A \r\n\n  B\n   ')).toEqual(['A', 'B'])
    expect(lines('')).toEqual([])
  })

  it('names attendees by label and member, and lists unanswered questions', () => {
    const m = meeting({
      attendees: [
        {
          label: 'P1',
          member: 'rev',
          name: 'Reviewer',
          rank: 'peer',
          backend: 'claude',
          present: true,
        },
        { label: 'P2', member: 'arch', rank: 'senior', backend: 'agy', present: false },
      ],
      leadQa: [
        { id: 'q1', round: 1, label: 'P1', question: 'Which DB?', answer: 'SQLite' },
        { id: 'q2', round: 1, label: 'P2', question: 'Keep v1?' },
      ],
    })
    expect(attendeeName(m, 'P1')).toBe('P1 · Reviewer')
    expect(attendeeName(m, 'P2')).toBe('P2 · arch')
    expect(attendeeName(m, 'P9')).toBe('P9')
    expect(pendingQuestions(m).map((q) => q.id)).toEqual(['q2'])
    // A meeting that went on (or ended) without the answer takes none any more.
    expect(pendingQuestions({ ...m, status: 'closed' })).toEqual([])
    expect(pendingQuestions({ ...m, status: 'round' })).toEqual([])
  })

  it('lets only meetings and contests that have not ended be cancelled', () => {
    expect(meetingOpen('round')).toBe(true)
    expect(meetingOpen('waiting_lead')).toBe(true)
    expect(meetingOpen('closed')).toBe(false)
    expect(meetingOpen('failed')).toBe(false)
    expect(contestOpen('ready')).toBe(true)
    expect(contestOpen('applied')).toBe(false)
    expect(contestOpen('cancelled')).toBe(false)
  })
})
