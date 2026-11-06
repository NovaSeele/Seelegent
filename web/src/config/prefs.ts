/**
 * Per-viewer UI preferences kept in the browser (not daemon settings). One storage key each.
 * The pre-paint script in index.html reads the `ui.*` and `layout.sidebarWidth` keys with the same ranges;
 * src/prepaint.spec.ts runs that script against these definitions.
 */
import { isLocale, type Locale } from '@/i18n/locales'
import { isBoolean, isNumberIn, isOneOf, type StoredValue } from '@/lib/storage'

import {
  isThemeChoice,
  MONO_FONTS,
  MOTION_PREFS,
  UI_FONTS,
  type MonoFont,
  type MotionPref,
  type ThemeChoice,
  type UiFont,
} from './themes'

export const RANGES = {
  sizeUi: { min: 13, max: 16, step: 0.5, fallback: 14 },
  sizeTerm: { min: 12, max: 17, step: 0.5, fallback: 13.5 },
  lineHeightTerm: { min: 1.3, max: 1.9, step: 0.05, fallback: 1.55 },
  sidebarWidth: { min: 240, max: 560, step: 16, fallback: 300 },
} as const

function pref<T>(
  name: string,
  fallback: T,
  validate: (value: unknown) => value is T,
): StoredValue<T> {
  return { name, fallback, validate }
}

const range = (r: { min: number; max: number }) => isNumberIn(r.min, r.max)

export const PREFS = {
  theme: pref<ThemeChoice>('ui.theme', 'auto', isThemeChoice),
  fontUi: pref<UiFont>('ui.fontUi', 'system', isOneOf(UI_FONTS)),
  fontMono: pref<MonoFont>('ui.fontMono', 'theme', isOneOf(MONO_FONTS)),
  sizeUi: pref('ui.sizeUi', RANGES.sizeUi.fallback as number, range(RANGES.sizeUi)),
  sizeTerm: pref('ui.sizeTerm', RANGES.sizeTerm.fallback as number, range(RANGES.sizeTerm)),
  lineHeightTerm: pref(
    'ui.lineHeightTerm',
    RANGES.lineHeightTerm.fallback as number,
    range(RANGES.lineHeightTerm),
  ),
  motion: pref<MotionPref>('ui.motion', 'system', isOneOf(MOTION_PREFS)),
  /** null = never chosen: follow the browser's languages. */
  locale: pref<Locale | null>(
    'ui.locale',
    null,
    (value: unknown): value is Locale | null => value === null || isLocale(value),
  ),
  sidebarWidth: pref(
    'layout.sidebarWidth',
    RANGES.sidebarWidth.fallback as number,
    range(RANGES.sidebarWidth),
  ),
  sidebarCollapsed: pref('layout.sidebarCollapsed', false, isBoolean),
} as const
