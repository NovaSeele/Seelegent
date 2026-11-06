/**
 * vue-i18n setup: seven locales, namespaced JSON catalogs under locales/<lang>/<ns>.json, loaded per locale on
 * demand. Switching language loads the catalogs, swaps the messages and updates <html lang>; no page reload.
 */
import { createI18n } from 'vue-i18n'

import type common from './locales/en/common.json'
import type locale from './locales/en/locale.json'
import type nav from './locales/en/nav.json'
import type pages from './locales/en/pages.json'
import type palette from './locales/en/palette.json'
import type settings from './locales/en/settings.json'
import type task from './locales/en/task.json'
import type tasks from './locales/en/tasks.json'
import type terminal from './locales/en/terminal.json'
import type watch from './locales/en/watch.json'
import { FALLBACK_LOCALE, PLURAL_FORMS, SUPPORTED_LOCALES, type Locale } from './locales'

export * from './locales'

export const NAMESPACES = [
  'common',
  'nav',
  'locale',
  'settings',
  'palette',
  'pages',
  'watch',
  'tasks',
  'task',
  'terminal',
] as const
export type Namespace = (typeof NAMESPACES)[number]

export interface MessageSchema {
  common: typeof common
  nav: typeof nav
  locale: typeof locale
  settings: typeof settings
  palette: typeof palette
  pages: typeof pages
  watch: typeof watch
  tasks: typeof tasks
  task: typeof task
  terminal: typeof terminal
}

declare module 'vue-i18n' {
  // eslint-disable-next-line @typescript-eslint/no-empty-object-type
  export interface DefineLocaleMessage extends MessageSchema {}
}

/** Picks the form for `choice` from a "one | few | many | other" message, per PLURAL_FORMS. */
export function pluralIndex(locale: Locale, choice: number, choicesLength: number): number {
  const forms = PLURAL_FORMS[locale]
  const category = new Intl.PluralRules(locale).select(Math.abs(choice))
  const index = forms.indexOf(category)
  return Math.min(index < 0 ? forms.length - 1 : index, Math.max(0, choicesLength - 1))
}

const pluralRules = Object.fromEntries(
  SUPPORTED_LOCALES.map((loc) => [
    loc,
    (choice: number, choicesLength: number) => pluralIndex(loc, choice, choicesLength),
  ]),
)

export function createAppI18n(
  locale: Locale = FALLBACK_LOCALE,
  messages: Partial<Record<Locale, MessageSchema>> = {},
) {
  return createI18n<[MessageSchema], Locale, false>({
    legacy: false,
    locale,
    fallbackLocale: FALLBACK_LOCALE,
    messages: messages as Record<Locale, MessageSchema>,
    pluralRules,
    missingWarn: import.meta.env.DEV,
    fallbackWarn: false,
  })
}

export type AppI18n = ReturnType<typeof createAppI18n>

const loaders = import.meta.glob<unknown>('./locales/*/*.json', { import: 'default' })

/** Loads every namespace of one locale (each locale is its own set of chunks). */
export async function loadMessages(loc: Locale): Promise<MessageSchema> {
  const entries = await Promise.all(
    NAMESPACES.map(async (ns) => {
      const load = loaders[`./locales/${loc}/${ns}.json`]
      if (!load) throw new Error(`missing catalog ${loc}/${ns}`)
      return [ns, await load()] as const
    }),
  )
  return Object.fromEntries(entries) as unknown as MessageSchema
}

export const i18n = createAppI18n()

/**
 * Makes `loc` the UI language: loads its catalogs (and the English fallback) once, then switches the reactive
 * locale and <html lang>. Safe to call repeatedly.
 */
export async function activateLocale(loc: Locale, target: AppI18n = i18n): Promise<void> {
  const g = target.global
  const needed = [...new Set([loc, FALLBACK_LOCALE])].filter(
    (l) => Object.keys(g.getLocaleMessage(l)).length === 0,
  )
  const loaded = await Promise.all(needed.map(async (l) => [l, await loadMessages(l)] as const))
  for (const [l, messages] of loaded) g.setLocaleMessage(l, messages)
  g.locale.value = loc
  if (typeof document !== 'undefined') document.documentElement.lang = loc
}
