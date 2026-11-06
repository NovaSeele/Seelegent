import { describe, expect, it } from 'vitest'

import { computePosition } from './useFloating'

const viewport = { width: 1000, height: 800 }
const size = { width: 200, height: 100 }

describe('computePosition', () => {
  it('places below and aligned to the start with the offset', () => {
    const anchor = { top: 100, left: 100, width: 80, height: 30 }
    expect(computePosition(anchor, size, viewport, 'bottom-start', 4)).toEqual({
      top: 134,
      left: 100,
      side: 'bottom',
    })
  })

  it('centres on the anchor and aligns ends', () => {
    const anchor = { top: 100, left: 400, width: 100, height: 30 }
    expect(computePosition(anchor, size, viewport, 'bottom', 0).left).toBe(350)
    expect(computePosition(anchor, size, viewport, 'bottom-end', 0).left).toBe(300)
  })

  it('flips to the other side when there is no room', () => {
    const anchor = { top: 740, left: 100, width: 80, height: 30 }
    const placed = computePosition(anchor, size, viewport, 'bottom-start', 4)
    expect(placed.side).toBe('top')
    expect(placed.top).toBe(636)
  })

  it('keeps the box inside the viewport margins', () => {
    const anchor = { top: 100, left: 950, width: 40, height: 30 }
    expect(computePosition(anchor, size, viewport, 'bottom-start', 4).left).toBe(792)
    const right = computePosition(
      { top: 5, left: 900, width: 40, height: 20 },
      size,
      viewport,
      'right',
      6,
    )
    expect(right.side).toBe('left')
    expect(right.top).toBe(8)
  })
})
