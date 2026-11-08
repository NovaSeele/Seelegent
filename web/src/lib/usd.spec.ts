import { describe, expect, it } from 'vitest'

import { formatUSD } from './usd'

describe('formatUSD', () => {
  it('writes amounts as the daemon does', () => {
    expect(formatUSD(200)).toBe('$200')
    expect(formatUSD(0)).toBe('$0')
    expect(formatUSD(0.155)).toBe('$0.155')
    expect(formatUSD(0.1)).toBe('$0.10')
    expect(formatUSD(0.081)).toBe('$0.081')
    expect(formatUSD(12.4)).toBe('$12.40')
  })
})
