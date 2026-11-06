import { describe, expect, it } from 'vitest'

import { bestScore, fuzzyScore, normalizeText } from './fuzzy'

describe('fuzzy search', () => {
  it('ignores case and diacritics, including Vietnamese đ', () => {
    expect(normalizeText('Tiến Trình')).toBe('tien trinh')
    expect(normalizeText('Đóng')).toBe('dong')
    expect(fuzzyScore('tien trinh', 'Tiến trình')).toBe(10000)
    expect(fuzzyScore('dong', 'Đóng thông báo')).toBeGreaterThan(0)
    expect(fuzzyScore('cai dat', 'Cài đặt')).toBe(10000)
  })

  it('ranks exact > prefix > word > word prefix > substring > scattered letters', () => {
    const scores = [
      fuzzyScore('theme', 'theme'),
      fuzzyScore('theme', 'theme picker'),
      fuzzyScore('theme', 'change theme'),
      fuzzyScore('them', 'change theme'),
      fuzzyScore('hem', 'change theme'),
      fuzzyScore('cht', 'change theme'),
    ]
    expect([...scores].sort((a, b) => b - a)).toEqual(scores)
    expect(scores.every((s) => s > 0)).toBe(true)
  })

  it('returns 0 when letters are missing or out of order', () => {
    expect(fuzzyScore('xyz', 'Settings')).toBe(0)
    expect(fuzzyScore('gnittes', 'settings')).toBe(0)
  })

  it('weights several texts', () => {
    expect(
      bestScore('tasks', [
        ['Tasks', 1],
        ['tasks', 0.5],
      ]),
    ).toBe(10000)
    expect(
      bestScore('tasks', [
        ['Tiến trình', 1],
        ['Tasks', 0.9],
      ]),
    ).toBe(9000)
  })
})
