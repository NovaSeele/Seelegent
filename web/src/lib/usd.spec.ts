import { describe, expect, it } from 'vitest'

import { formatMoney, formatUSD } from './usd'

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

describe('formatMoney', () => {
  it('writes the currency the service sent, US dollars as formatUSD does', () => {
    expect(formatMoney(12.4, 'USD')).toBe('$12.40')
    expect(formatMoney(187.6, 'CNY')).toBe('¥187.60')
    expect(formatMoney(3, 'credits')).toBe('3.00 credits')
  })
})
