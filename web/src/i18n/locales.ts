/** Supported UI languages. Ids are BCP 47 tags and go straight into <html lang>. */
export const SUPPORTED_LOCALES = ['vi', 'en', 'zh-CN', 'es', 'ko', 'ja', 'ru'] as const
export type Locale = (typeof SUPPORTED_LOCALES)[number]

export const FALLBACK_LOCALE: Locale = 'en'

/** Each language's own name (endonym): the same in every UI language, so it is data, not a translation. */
export const LOCALE_NAMES: Readonly<Record<Locale, string>> = {
  vi: 'Tiếng Việt',
  en: 'English',
  'zh-CN': '简体中文',
  es: 'Español',
  ko: '한국어',
  ja: '日本語',
  ru: 'Русский',
}

/**
 * Plural forms each catalog writes for a counted message, in this order, separated by " | ".
 * Categories come from Intl.PluralRules; a category a locale does not list falls back to its last form.
 */
export const PLURAL_FORMS: Readonly<Record<Locale, readonly Intl.LDMLPluralRule[]>> = {
  vi: ['other'],
  en: ['one', 'other'],
  'zh-CN': ['other'],
  es: ['one', 'other'],
  ko: ['other'],
  ja: ['other'],
  ru: ['one', 'few', 'many', 'other'],
}

export function isLocale(value: unknown): value is Locale {
  return typeof value === 'string' && (SUPPORTED_LOCALES as readonly string[]).includes(value)
}

/**
 * Maps a browser or legacy language tag to a supported locale: exact match first, then the base language
 * (`zh-TW` and the old dashboard's `zh` map to zh-CN, `en-GB` to en).
 */
export function matchLocale(tag: string): Locale | null {
  const lower = tag.trim().toLowerCase()
  if (!lower) return null
  const exact = SUPPORTED_LOCALES.find((locale) => locale.toLowerCase() === lower)
  if (exact) return exact
  const base = lower.split(/[-_]/)[0]
  return SUPPORTED_LOCALES.find((locale) => locale.toLowerCase().split('-')[0] === base) ?? null
}

/** Picks the first supported locale from the browser's preference list, else the fallback. */
export function detectLocale(preferred: readonly string[]): Locale {
  for (const tag of preferred) {
    const match = matchLocale(tag)
    if (match) return match
  }
  return FALLBACK_LOCALE
}
