import { beforeEach, describe, expect, it } from 'vitest'
import { nextTick } from 'vue'

import { PREFS } from '@/config/prefs'
import { i18n } from '@/i18n'
import { storageKey } from '@/lib/storage'
import { setupApp } from '@/test/app'

import { useUiStore } from './ui'

describe('ui store', () => {
  beforeEach(async () => {
    localStorage.clear()
    await setupApp()
  })

  it('applies appearance to <html> and saves each pref under its own key', async () => {
    const ui = useUiStore()
    ui.setTheme('dracula')
    ui.setMotion('off')
    ui.setFontUi('inter')
    ui.sizeUi = 15
    await nextTick()
    const root = document.documentElement
    expect(root.dataset.theme).toBe('dracula')
    expect(root.dataset.motion).toBe('off')
    expect(root.dataset.fontUi).toBe('inter')
    expect(root.style.getPropertyValue('--font-size-ui')).toBe('15px')
    expect(localStorage.getItem(storageKey(PREFS.theme.name))).toBe('"dracula"')
    expect(localStorage.getItem(storageKey(PREFS.motion.name))).toBe('"off"')
    expect(localStorage.getItem(storageKey(PREFS.sizeUi.name))).toBe('15')
  })

  it('resolves Auto to Seelegent Paper or Ink', async () => {
    const ui = useUiStore()
    expect(ui.theme).toBe('auto')
    // jsdom has no matchMedia: prefers-color-scheme: light is false, so Auto is the dark theme.
    expect(ui.resolvedTheme).toBe('seelegent-dark')
  })

  it('clamps the sidebar width and resets appearance', () => {
    const ui = useUiStore()
    ui.setSidebarWidth(9999)
    expect(ui.sidebarWidth).toBe(560)
    ui.setSidebarWidth(10)
    expect(ui.sidebarWidth).toBe(240)
    ui.setTheme('nord')
    ui.resetAppearance()
    expect(ui.theme).toBe('auto')
  })

  it('switches language without reloading and can go back to the default, English', async () => {
    const ui = useUiStore()
    await ui.setLocale('ko')
    expect(i18n.global.locale.value).toBe('ko')
    expect(document.documentElement.lang).toBe('ko')
    expect(localStorage.getItem(storageKey(PREFS.locale.name))).toBe('"ko"')
    await ui.setLocale(null)
    expect(ui.localeChoice).toBeNull()
    expect(ui.locale).toBe('en')
  })

  it('hands a task search request to exactly one taker', () => {
    const ui = useUiStore()
    expect(ui.takeTaskSearchRequest()).toBe(false)
    ui.requestTaskSearch()
    expect(ui.taskSearchRequested).toBe(true)
    expect(ui.takeTaskSearchRequest()).toBe(true)
    expect(ui.takeTaskSearchRequest()).toBe(false)
  })
})
