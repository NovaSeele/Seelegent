import { describe, expect, it } from 'vitest'

import type { History } from '@/api/types'

import { change, historyAccounts, linePath, niceMax, quotaSeries } from './usageModel'

describe('usage model', () => {
  it('computes the change against the previous period', () => {
    expect(change(150, 100)).toBe(0.5)
    expect(change(50, 100)).toBe(-0.5)
    expect(change(0, 0)).toBe(0)
    expect(change(10, 0)).toBeNull()
  })

  it('rounds an axis up to 1, 2, 2.5 or 5 times a power of ten', () => {
    expect(niceMax(0)).toBe(1)
    expect(niceMax(7)).toBe(10)
    expect(niceMax(180_000)).toBe(200_000)
    expect(niceMax(2_100)).toBe(2_500)
    expect(niceMax(420)).toBe(500)
  })

  const history: History = {
    bucket: 'hour',
    since: '2026-11-01T00:00:00Z',
    until: '2026-11-01T02:00:00Z',
    rows: [
      {
        at: '2026-11-01T01:00:00Z',
        accountId: 'a1',
        backend: 'agy',
        label: 'Work',
        enabled: true,
        status: 'ok',
        quota: [{ group: 'pro', window: '5h', remaining: 0.4 }],
        tokensToday: 0,
        costToday: 0,
      },
      {
        at: '2026-11-01T00:00:00Z',
        accountId: 'a1',
        backend: 'agy',
        label: 'Work',
        enabled: true,
        status: 'ok',
        quota: [{ group: 'pro', window: '5h', remaining: 0.9 }],
        tokensToday: 0,
        costToday: 0,
      },
      {
        at: '2026-11-01T00:00:00Z',
        accountId: 'a2',
        backend: 'claude',
        label: 'Max',
        enabled: true,
        status: 'ok',
        quota: [],
        tokensToday: 0,
        costToday: 0,
      },
    ],
  }

  it('builds one sorted line per window of an account', () => {
    expect(historyAccounts(history).map((a) => a.id)).toEqual(['a1', 'a2'])
    const [line] = quotaSeries(history, 'a1')
    expect(line?.key).toBe('pro · 5h')
    expect(line?.points.map((p) => p.v)).toEqual([0.9, 0.4])
    expect(line?.low).toBe(0.4)
  })

  it('reads the lowest remaining of each day from daily rollups', () => {
    const days: History = {
      bucket: 'day',
      since: '',
      until: '',
      days: [
        {
          date: '2026-11-01',
          accountId: 'a1',
          backend: 'agy',
          label: 'Work',
          enabled: true,
          status: 'ok',
          samples: 24,
          firstAt: '',
          lastAt: '',
          quota: [{ group: 'pro', window: '5h', remaining: 0.8, minRemaining: 0.1 }],
          tokensToday: 0,
          costToday: 0,
        },
      ],
    }
    expect(quotaSeries(days, 'a1')[0]?.points[0]?.v).toBe(0.1)
  })

  it('draws a path inside the box', () => {
    expect(
      linePath(
        [
          { t: 0, v: 1 },
          { t: 10, v: 0 },
        ],
        0,
        10,
        100,
        50,
      ),
    ).toBe('M0.0 0.0 L100.0 50.0')
  })
})
