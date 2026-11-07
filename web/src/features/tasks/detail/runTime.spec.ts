import { describe, expect, it } from 'vitest'

import { runTime } from './runTime'

describe('runTime', () => {
  it('adds the turns and leaves out the waits between them and after the last', () => {
    const turns = [
      { startedAt: '2026-11-08T01:00:00Z', endedAt: '2026-11-08T01:00:07Z' },
      // 19 minutes of review later, a followup ran for 3 s.
      { startedAt: '2026-11-08T01:19:00Z', endedAt: '2026-11-08T01:19:03Z' },
    ]
    expect(runTime(turns, Date.parse('2026-11-08T02:00:00Z'))).toBe(10_000)
  })

  it('counts a running turn up to now and has nothing to say without turns', () => {
    const now = Date.parse('2026-11-08T01:00:05Z')
    expect(runTime([{ startedAt: '2026-11-08T01:00:00Z' }], now)).toBe(5_000)
    expect(runTime([], now)).toBeNull()
    expect(runTime([{ startedAt: 'nonsense' }], now)).toBeNull()
  })
})
