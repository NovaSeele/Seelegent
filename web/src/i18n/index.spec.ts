import { describe, expect, it } from 'vitest'

import { detectLocale, fallbackLocale } from '.'

describe('detectLocale', () => {
  it('matches the base language of the first supported tag', () => {
    expect(detectLocale(['fr-FR', 'vi-VN', 'en-US'])).toBe('vi')
    expect(detectLocale(['EN-gb'])).toBe('en')
  })

  it('falls back when nothing is supported', () => {
    expect(detectLocale(['fr', 'de'])).toBe(fallbackLocale)
    expect(detectLocale([])).toBe(fallbackLocale)
  })
})
