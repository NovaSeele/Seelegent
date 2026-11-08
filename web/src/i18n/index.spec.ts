import { afterEach, describe, expect, it, vi } from 'vitest'

import { useUiStore } from '@/stores/ui'
import { setupApp } from '@/test/app'

import { activateLocale, createAppI18n, FALLBACK_LOCALE, matchLocale, pluralIndex } from '.'

describe('default locale', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    localStorage.clear()
  })

  it('is English with no stored choice, whatever languages the browser prefers', async () => {
    localStorage.clear()
    vi.spyOn(navigator, 'languages', 'get').mockReturnValue(['vi-VN'])
    vi.spyOn(navigator, 'language', 'get').mockReturnValue('vi-VN')
    await setupApp()
    const ui = useUiStore()
    expect(FALLBACK_LOCALE).toBe('en')
    expect(ui.localeChoice).toBeNull()
    expect(ui.locale).toBe('en')
  })

  it('keeps the language chosen in Settings', async () => {
    vi.spyOn(navigator, 'languages', 'get').mockReturnValue(['vi-VN'])
    await setupApp()
    const ui = useUiStore()
    await ui.setLocale('ja')
    expect(ui.locale).toBe('ja')
    await ui.setLocale(null)
    expect(ui.locale).toBe('en')
  })
})

describe('matchLocale', () => {
  it('maps every Chinese variant to zh-CN', () => {
    expect(matchLocale('zh')).toBe('zh-CN')
    expect(matchLocale('zh-TW')).toBe('zh-CN')
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
