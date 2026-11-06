import { createI18n } from 'vue-i18n'

import enApp from './locales/en/app.json'
import viApp from './locales/vi/app.json'

export const supportedLocales = ['en', 'vi'] as const
export type Locale = (typeof supportedLocales)[number]

export const fallbackLocale: Locale = 'en'

type MessageSchema = { app: typeof enApp }

declare module 'vue-i18n' {
  // eslint-disable-next-line @typescript-eslint/no-empty-object-type
  export interface DefineLocaleMessage extends MessageSchema {}
}

export const messages: Record<Locale, MessageSchema> = {
  en: { app: enApp },
  vi: { app: viApp },
}

/** Picks the first supported locale from the browser's preferences. */
export function detectLocale(preferred: readonly string[]): Locale {
  for (const tag of preferred) {
    const base = tag.toLowerCase().split('-')[0]
    const match = supportedLocales.find((locale) => locale === base)
    if (match) return match
  }
  return fallbackLocale
}

export function createAppI18n(locale: Locale = fallbackLocale) {
  return createI18n<[MessageSchema], Locale, false>({
    legacy: false,
    locale,
    fallbackLocale,
    messages,
  })
}

export const i18n = createAppI18n(detectLocale(navigator.languages))
