/**
 * Catalog parity: every locale has exactly the keys of English, the same {placeholders} in each message, the
 * plural form count its language needs, and every message renders.
 */
import { describe, expect, it } from 'vitest'

import { NAV_IDS, navItem } from '@/config/nav'
import { SECTION_IDS } from '@/features/settings/sections'

import { createAppI18n, NAMESPACES, PLURAL_FORMS, SUPPORTED_LOCALES, type Locale } from '.'

const files = import.meta.glob<Record<string, unknown>>('./locales/*/*.json', {
  eager: true,
  import: 'default',
})

type Flat = Record<string, string>

function flatten(node: unknown, prefix: string, out: Flat): Flat {
  if (typeof node === 'string') out[prefix] = node
  else if (node && typeof node === 'object') {
    for (const [key, value] of Object.entries(node))
      flatten(value, prefix ? `${prefix}.${key}` : key, out)
  } else throw new Error(`${prefix}: not a string`)
  return out
}

function catalog(loc: Locale): Flat {
  const out: Flat = {}
  for (const ns of NAMESPACES) {
    const messages = files[`./locales/${loc}/${ns}.json`]
    if (!messages) throw new Error(`missing ${loc}/${ns}.json`)
    flatten(messages, ns, out)
  }
  return out
}

const placeholders = (message: string) =>
  [...message.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort()
const isPlural = (message: string) => message.includes(' | ')
const sameName = (a: string, b: string) =>
  a.trim().toLocaleLowerCase() === b.trim().toLocaleLowerCase()

const en = catalog('en')

describe('catalogs', () => {
  it('has a file for every namespace of every locale, and nothing else', () => {
    const expected = SUPPORTED_LOCALES.flatMap((loc) =>
      NAMESPACES.map((ns) => `./locales/${loc}/${ns}.json`),
    )
    expect(Object.keys(files).sort()).toEqual(expected.sort())
  })

  describe.each(SUPPORTED_LOCALES.filter((loc) => loc !== 'en'))('%s', (loc) => {
    const messages = catalog(loc)

    it('has the same keys as English', () => {
      expect(Object.keys(messages).sort()).toEqual(Object.keys(en).sort())
    })

    it('keeps every placeholder of each message', () => {
      const wrong = Object.keys(en).filter((key) => {
        const theirs = messages[key]
        if (theirs === undefined) return false
        const forms = isPlural(theirs) ? theirs.split(' | ') : [theirs]
        const expected = placeholders(en[key] as string).filter((name) => name !== 'count')
        return forms.some((form) => {
          const found = placeholders(form).filter((name) => name !== 'count')
          return expected.some((name) => !found.includes(name))
        })
      })
      expect(wrong).toEqual([])
    })

    it('has the plural forms its language needs', () => {
      const wanted = PLURAL_FORMS[loc].length
      const wrong = Object.entries(en)
        .filter(([, message]) => isPlural(message))
        .map(([key]) => key)
        .filter((key) => (messages[key] ?? '').split(' | ').length !== wanted)
      expect(wrong).toEqual([])
    })
  })

  it('gives each page one name: no short or alternative titles that could drift apart', () => {
    for (const id of NAV_IDS) {
      const keys = Object.keys(en).filter((key) => key.startsWith(`nav.${id}.`))
      const allowed = navItem(id).keywords ? ['desc', 'keywords', 'label'] : ['desc', 'label']
      expect(keys.map((key) => key.slice(`nav.${id}.`.length)).sort(), id).toEqual(allowed)
    }
  })

  // The rail's label box is 68px of 11px text: about 11 Latin or Cyrillic letters, or 5 CJK characters (which are
  // about twice as wide). A longer word would be cut mid-word, so pick a shorter page name instead.
  it.each(SUPPORTED_LOCALES)('%s page names fit the rail word by word', (loc) => {
    const flat = catalog(loc)
    const width = (word: string) =>
      [...word].reduce(
        (sum, ch) =>
          sum +
          (/[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}]/u.test(ch)
            ? 2
            : 1),
        0,
      )
    const tooWide = NAV_IDS.flatMap((id) => (flat[`nav.${id}.label`] ?? '').split(/\s+/)).filter(
      (word) => width(word) > 11,
    )
    expect(tooWide).toEqual([])
  })

  // Pages and settings sections share the palette: two entries with one name that go to different places
  // cannot be told apart, so every name must be unique within its kind and across the two kinds.
  it.each(SUPPORTED_LOCALES)('%s names every page and settings section differently', (loc) => {
    const flat = catalog(loc)
    const pages = NAV_IDS.map((id) => flat[`nav.${id}.label`] ?? '')
    const sections = SECTION_IDS.map((id) => flat[`settings.sections.${id}.label`] ?? '')
    const dupes = (names: string[]) =>
      names.filter((name, i) => names.findIndex((other) => sameName(other, name)) !== i)
    expect(dupes(pages), 'pages').toEqual([])
    expect(dupes(sections), 'settings sections').toEqual([])
    expect(
      pages.filter((page) => sections.some((section) => sameName(section, page))),
      'pages named like a settings section',
    ).toEqual([])
  })

  it('renders every message in every locale', () => {
    for (const loc of SUPPORTED_LOCALES) {
      const flat = catalog(loc)
      const i18n = createAppI18n(loc, {
        [loc]: Object.fromEntries(
          NAMESPACES.map((ns) => [ns, files[`./locales/${loc}/${ns}.json`]]),
        ),
      } as never)
      for (const [key, message] of Object.entries(flat)) {
        const params = Object.fromEntries(placeholders(message).map((name) => [name, 'X']))
        const text = isPlural(message)
          ? i18n.global.t(key, 2, { named: params })
          : i18n.global.t(key, params)
        expect(text, `${loc}:${key}`).not.toMatch(/[{}]/)
        expect(text, `${loc}:${key}`).not.toBe(key)
      }
    }
  })
})
