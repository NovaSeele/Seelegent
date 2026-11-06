import { describe, expect, it } from 'vitest'

import { fromBottom, indexAt, keepsFollowing, layout, visibleRange } from './virtual'

describe('virtual list layout', () => {
  const keys = ['a', 'b', 'c', 'd', 'e']
  const offsets = layout(
    keys,
    new Map([
      ['b', 100],
      ['d', 10],
    ]),
    40,
  )

  it('adds measured heights and estimates for the rest', () => {
    expect(offsets).toEqual([0, 40, 140, 180, 190, 230])
    expect(layout([], new Map(), 40)).toEqual([0])
  })

  it('finds the item at a given height', () => {
    expect(indexAt(offsets, 0)).toBe(0)
    expect(indexAt(offsets, 39)).toBe(0)
    expect(indexAt(offsets, 40)).toBe(1)
    expect(indexAt(offsets, 185)).toBe(3)
    expect(indexAt(offsets, 9999)).toBe(4)
  })

  it('returns the overlapping range with overscan, never empty for a non-empty list', () => {
    expect(visibleRange(offsets, 50, 60, 0)).toEqual({ start: 1, end: 2 })
    expect(visibleRange(offsets, 50, 100, 0)).toEqual({ start: 1, end: 3 })
    expect(visibleRange(offsets, 150, 10, 20)).toEqual({ start: 1, end: 4 })
    expect(visibleRange(offsets, 5000, 100, 0)).toEqual({ start: 4, end: 5 })
    expect(visibleRange([0], 0, 100, 0)).toEqual({ start: 0, end: 0 })
  })

  it('measures the distance to the end', () => {
    expect(fromBottom({ scrollTop: 100, scrollHeight: 500, clientHeight: 300 })).toBe(100)
  })
})

describe('following the end', () => {
  const el = (scrollTop: number, scrollHeight = 1000) => ({
    scrollTop,
    scrollHeight,
    clientHeight: 200,
  })

  it('keeps following when content grows below a programmatic jump to the end', () => {
    // Jumped to 800 (the end); items were then measured taller, so the end moved to 1400.
    expect(keepsFollowing(true, 800, el(800, 1400), 48)).toBe(true)
  })

  it('stops only when the reader scrolls up, and resumes at the end', () => {
    expect(keepsFollowing(true, 800, el(600), 48)).toBe(false)
    expect(keepsFollowing(false, 600, el(700), 48)).toBe(false)
    expect(keepsFollowing(false, 700, el(790), 48)).toBe(true)
  })
})
