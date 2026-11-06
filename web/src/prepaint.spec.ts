/**
 * Runs the pre-paint script from index.html (as the build fills it in) against jsdom and checks it applies the
 * preferences written by src/config/prefs.ts, rejects bad values and never throws.
 */
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import { PREFS, RANGES } from '@/config/prefs'
import { THEMES } from '@/config/themes'
import { SUPPORTED_LOCALES } from '@/i18n/locales'
import { storageKey, writeStored, type StoredValue } from '@/lib/storage'

import { readFile } from '@/test/files'

const html = readFile('index.html')

const script = (() => {
  const match = /<script>([\s\S]*?)<\/script>/.exec(html)
  if (!match?.[1]) throw new Error('pre-paint script not found')
  const ids = JSON.stringify(THEMES.map((theme) => theme.id))
  return match[1].replace('/*seelegent:themes*/ []', ids)
})()

function setMedia(matching: string[]): void {
  window.matchMedia = ((query: string) =>
    ({ matches: matching.includes(query) }) as MediaQueryList) as typeof window.matchMedia
}

function run(): HTMLElement {
  new Function(script)()
  return document.documentElement
}

function save<T>(spec: StoredValue<T>, value: unknown): void {
  window.localStorage.setItem(storageKey(spec.name), JSON.stringify(value))
}

describe('pre-paint script', () => {
  beforeEach(() => {
    window.localStorage.clear()
    const root = document.documentElement
    for (const attr of [...root.attributes]) root.removeAttribute(attr.name)
    setMedia([])
  })
  afterEach(() => window.localStorage.clear())

  it('lists the same locales as the app', () => {
    expect(script).toContain(
      JSON.stringify(SUPPORTED_LOCALES).replace(/"/g, "'").replace(/,/g, ', '),
    )
  })

  it('defaults to Seelegent Ink, or Paper when the OS prefers light', () => {
    expect(run().dataset.theme).toBe('seelegent-dark')
    setMedia(['(prefers-color-scheme: light)'])
    expect(run().dataset.theme).toBe('seelegent-light')
  })

  it('applies every saved preference', () => {
    writeStored(PREFS.theme, 'vscode-light-modern')
    writeStored(PREFS.motion, 'off')
    writeStored(PREFS.fontUi, 'inter')
    writeStored(PREFS.fontMono, 'jetbrains-mono')
    writeStored(PREFS.sizeUi, 15.5)
    writeStored(PREFS.sizeTerm, 12)
    writeStored(PREFS.lineHeightTerm, 1.7)
    writeStored(PREFS.sidebarWidth, 420)
    writeStored(PREFS.locale, 'ja')
    const root = run()
    expect(root.dataset.theme).toBe('vscode-light-modern')
    expect(root.dataset.motion).toBe('off')
    expect(root.dataset.fontUi).toBe('inter')
    expect(root.dataset.fontMono).toBe('jetbrains-mono')
    expect(root.style.getPropertyValue('--font-size-ui')).toBe('15.5px')
    expect(root.style.getPropertyValue('--font-size-term')).toBe('12px')
    expect(root.style.getPropertyValue('--line-height-term')).toBe('1.7')
    expect(root.style.getPropertyValue('--sidebar-width')).toBe('420px')
    expect(root.lang).toBe('ja')
  })

  it('resolves auto and the system motion setting', () => {
    writeStored(PREFS.theme, 'auto')
    setMedia(['(prefers-reduced-motion: reduce)'])
    const root = run()
    expect(root.dataset.theme).toBe('seelegent-dark')
    expect(root.dataset.motion).toBe('off')
    writeStored(PREFS.motion, 'on')
    expect(run().dataset.motion).toBe('on')
  })

  it('ignores unknown, out-of-range and corrupt values', () => {
    save(PREFS.theme, 'no-such-theme')
    save(PREFS.sizeUi, RANGES.sizeUi.max + 1)
    save(PREFS.sidebarWidth, '300')
    save(PREFS.fontUi, 'x" onload="alert(1)')
    save(PREFS.locale, 'fr')
    window.localStorage.setItem(storageKey(PREFS.motion.name), '{not json')
    const root = run()
    expect(root.dataset.theme).toBe('seelegent-dark')
    expect(root.dataset.motion).toBe('on')
    expect(root.hasAttribute('data-font-ui')).toBe(false)
    expect(root.style.getPropertyValue('--font-size-ui')).toBe('')
    expect(root.style.getPropertyValue('--sidebar-width')).toBe('')
    expect(root.hasAttribute('lang')).toBe(false)
  })

  it('survives a browser without storage or matchMedia', () => {
    const storage = Object.getOwnPropertyDescriptor(window, 'localStorage')
    Object.defineProperty(window, 'localStorage', {
      configurable: true,
      get() {
        throw new Error('blocked')
      },
    })
    // @ts-expect-error simulate a browser without matchMedia
    window.matchMedia = undefined
    try {
      expect(run().dataset.theme).toBe('seelegent-dark')
    } finally {
      if (storage) Object.defineProperty(window, 'localStorage', storage)
    }
  })
})
