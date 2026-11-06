import { describe, expect, it } from 'vitest'

import {
  activateLocale,
  createAppI18n,
  detectLocale,
  FALLBACK_LOCALE,
  matchLocale,
  pluralIndex,
} from '.'

describe('detectLocale', () => {
  it('matches the base language of the first supported tag', () => {
    expect(detectLocale(['fr-FR', 'vi-VN', 'en-US'])).toBe('vi')
    expect(detectLocale(['EN-gb'])).toBe('en')
  })

  it('maps every Chinese variant to zh-CN', () => {
    expect(matchLocale('zh')).toBe('zh-CN')
    expect(matchLocale('zh-TW')).toBe('zh-CN')
  })

  it('falls back when nothing is supported', () => {
    expect(detectLocale(['fr', 'de'])).toBe(FALLBACK_LOCALE)
    expect(detectLocale([])).toBe(FALLBACK_LOCALE)
  })
})

describe('pluralIndex', () => {
  it('picks Russian one / few / many forms', () => {
    expect(pluralIndex('ru', 1, 4)).toBe(0)
    expect(pluralIndex('ru', 3, 4)).toBe(1)
    expect(pluralIndex('ru', 5, 4)).toBe(2)
    expect(pluralIndex('ru', 1.5, 4)).toBe(3)
  })

  it('uses the single form for languages without plurals', () => {
    expect(pluralIndex('ja', 1, 1)).toBe(0)
    expect(pluralIndex('vi', 7, 1)).toBe(0)
  })
})

describe('activateLocale', () => {
  it('loads catalogs, switches language and sets <html lang> without a reload', async () => {
    const i18n = createAppI18n()
    await activateLocale('ru', i18n)
    expect(i18n.global.locale.value).toBe('ru')
    expect(document.documentElement.lang).toBe('ru')
    expect(i18n.global.t('nav.badge.watch', 5)).toContain('5')
    expect(i18n.global.t('settings.title')).toBe('Настройки')
    await activateLocale('vi', i18n)
    expect(i18n.global.t('settings.title')).toBe('Cài đặt')
  })
})
