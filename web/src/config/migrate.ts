/**
 * One-time import of the old dashboard's appearance and language settings (same origin, so the same
 * localStorage). Runs before the stores read their preferences; a key that already exists in the new scheme
 * always wins. The old keys are left in place so going back to the old dashboard keeps working.
 */
import { matchLocale } from '@/i18n/locales'
import { readLegacy, readStored, storageKey, writeStored, type StoredValue } from '@/lib/storage'

import { PREFS } from './prefs'
import { MONO_FONTS, UI_FONTS } from './themes'

const LEGACY = {
  theme: 'seelegent_theme_v2',
  themeV1: 'seelegent_theme',
  locale: 'seelegent_locale',
  sidebarWidth: 'seelegent_sidebar_width',
} as const

const MIGRATED: StoredValue<boolean> = {
  name: 'migratedLegacy',
  fallback: false,
  validate: (value: unknown): value is boolean => typeof value === 'boolean',
}

/** Old font ids were display names ("IBM Plex Sans"); the new ids are kebab-case. */
function fontId(name: unknown): string | null {
  return typeof name === 'string' ? name.trim().toLowerCase().replace(/\s+/g, '-') : null
}

function setIfValid<T>(spec: StoredValue<T>, value: unknown): void {
  if (spec.validate(value)) writeStored(spec, value)
}

function parseObject(raw: string | null): Record<string, unknown> | null {
  if (!raw) return null
  try {
    const value: unknown = JSON.parse(raw)
    if (typeof value === 'string') return { themeId: value }
    return value && typeof value === 'object' ? (value as Record<string, unknown>) : null
  } catch {
    return { themeId: raw }
  }
}

export function migrateLegacyPrefs(): void {
  if (readStored(MIGRATED)) return
  // Only fill keys that the new dashboard has never written.
  const fresh = <T>(spec: StoredValue<T>) => readLegacyNew(spec) === null

  let legacy = parseObject(readLegacy(LEGACY.theme))
  if (!legacy) {
    const v1 = readLegacy(LEGACY.themeV1)
    if (v1 === 'light') legacy = { themeId: 'claude-light' }
    else if (v1 === 'dark') legacy = { themeId: 'claude-dark' }
  }
  if (legacy) {
    if (fresh(PREFS.theme)) setIfValid(PREFS.theme, legacy.themeId)
    if (fresh(PREFS.motion)) setIfValid(PREFS.motion, legacy.motion)
    if (fresh(PREFS.sizeUi)) setIfValid(PREFS.sizeUi, Number(legacy.uiSize))
    if (fresh(PREFS.sizeTerm)) setIfValid(PREFS.sizeTerm, Number(legacy.termSize))
    if (fresh(PREFS.lineHeightTerm)) setIfValid(PREFS.lineHeightTerm, Number(legacy.lineHeight))
    const ui = fontId(legacy.uiFont)
    if (fresh(PREFS.fontUi) && ui && (UI_FONTS as readonly string[]).includes(ui)) {
      setIfValid(PREFS.fontUi, ui)
    }
    const mono = fontId(legacy.monoFont)
    if (fresh(PREFS.fontMono) && mono && (MONO_FONTS as readonly string[]).includes(mono)) {
      setIfValid(PREFS.fontMono, mono)
    }
  }

  const locale = readLegacy(LEGACY.locale)
  if (locale && fresh(PREFS.locale)) setIfValid(PREFS.locale, matchLocale(locale))

  const width = readLegacy(LEGACY.sidebarWidth)
  if (width && fresh(PREFS.sidebarWidth)) setIfValid(PREFS.sidebarWidth, Number(width))

  writeStored(MIGRATED, true)
}

/** The raw new-scheme value, or null when the key was never written. */
function readLegacyNew(spec: StoredValue<unknown>): string | null {
  return readLegacy(storageKey(spec.name))
}
