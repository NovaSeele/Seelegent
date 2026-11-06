import { afterEach, describe, expect, it, vi } from 'vitest'

import { migrateLegacyPrefs } from '@/config/migrate'
import { PREFS } from '@/config/prefs'

import { isNumberIn, readStored, storageKey, writeStored, type StoredValue } from './storage'

const width: StoredValue<number> = {
  name: 'test.width',
  fallback: 300,
  validate: isNumberIn(240, 560),
}

afterEach(() => {
  window.localStorage.clear()
  vi.restoreAllMocks()
})

describe('storage', () => {
  it('round-trips a value under its namespaced key', () => {
    expect(writeStored(width, 400)).toBe(true)
    expect(window.localStorage.getItem('seelegent.v1.test.width')).toBe('400')
    expect(readStored(width)).toBe(400)
  })

  it('falls back on missing, corrupt or invalid values', () => {
    expect(readStored(width)).toBe(300)
    window.localStorage.setItem(storageKey(width.name), '{oops')
    expect(readStored(width)).toBe(300)
    window.localStorage.setItem(storageKey(width.name), '9000')
    expect(readStored(width)).toBe(300)
  })

  it('does not throw when storage refuses', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('full', 'QuotaExceededError')
    })
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('denied', 'SecurityError')
    })
    expect(writeStored(width, 400)).toBe(false)
    expect(readStored(width)).toBe(300)
  })

  it('gives every preference its own key', () => {
    const names = Object.values(PREFS).map((spec) => spec.name)
    expect(new Set(names).size).toBe(names.length)
  })
})

describe('migrateLegacyPrefs', () => {
  it('imports the old appearance, language and sidebar width once', () => {
    window.localStorage.setItem(
      'seelegent_theme_v2',
      JSON.stringify({
        themeId: 'nord',
        uiFont: 'IBM Plex Sans',
        monoFont: 'theme',
        uiSize: 15,
        termSize: 13,
        lineHeight: 1.6,
        motion: 'off',
      }),
    )
    window.localStorage.setItem('seelegent_locale', 'zh')
    window.localStorage.setItem('seelegent_sidebar_width', '360')
    migrateLegacyPrefs()
    expect(readStored(PREFS.theme)).toBe('nord')
    expect(readStored(PREFS.fontUi)).toBe('ibm-plex-sans')
    expect(readStored(PREFS.fontMono)).toBe('theme')
    expect(readStored(PREFS.sizeUi)).toBe(15)
    expect(readStored(PREFS.sizeTerm)).toBe(13)
    expect(readStored(PREFS.lineHeightTerm)).toBe(1.6)
    expect(readStored(PREFS.motion)).toBe('off')
    expect(readStored(PREFS.locale)).toBe('zh-CN')
    expect(readStored(PREFS.sidebarWidth)).toBe(360)

    // A later change in the new dashboard is never overwritten.
    writeStored(PREFS.theme, 'dracula')
    migrateLegacyPrefs()
    expect(readStored(PREFS.theme)).toBe('dracula')
  })

  it('keeps values already chosen in the new dashboard', () => {
    writeStored(PREFS.theme, 'monokai')
    window.localStorage.setItem('seelegent_theme', 'light')
    migrateLegacyPrefs()
    expect(readStored(PREFS.theme)).toBe('monokai')
  })
})
