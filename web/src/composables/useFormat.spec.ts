import { describe, expect, it } from 'vitest'

import { createFormatters } from './useFormat'

describe('formatters', () => {
  it('formats numbers and percentages per locale', () => {
    expect(createFormatters('en').number(1234.5)).toBe('1,234.5')
    expect(createFormatters('vi').number(1234.5)).toBe('1.234,5')
    expect(createFormatters('en').percent(0.425)).toBe('43%')
    expect(createFormatters('en').compact(12_300)).toBe('12.3K')
  })

  it('formats relative time with words for near values', () => {
    const now = Date.UTC(2026, 0, 2)
    const en = createFormatters('en')
    expect(en.relative(now - 86_400_000, now)).toBe('yesterday')
    expect(en.relative(now - 5 * 60_000, now)).toBe('5 minutes ago')
    expect(en.relative(now + 2 * 3_600_000, now)).toBe('in 2 hours')
    expect(createFormatters('ru').relative(now - 86_400_000, now)).toBe('вчера')
  })

  it('formats durations with at most two units', () => {
    const en = createFormatters('en')
    expect(en.duration(0)).toBe('0s')
    expect(en.duration(65_000)).toBe('1m 5s')
    expect(en.duration(3_725_000)).toBe('1h 2m')
    expect(en.duration(90_061_000)).toBe('1d 1h')
  })

  it('reuses one formatter set per locale', () => {
    expect(createFormatters('ja')).toBe(createFormatters('ja'))
  })
})
