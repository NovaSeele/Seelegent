import { describe, expect, it } from 'vitest'

import { setupApp } from '@/test/app'

import { FIELDS, isFieldSection } from './fields'
import { searchSettings } from './search'
import { SECTIONS, sectionInfo } from './sections'

describe('settings search', () => {
  it('puts settings whose own name matches first', async () => {
    await setupApp('en')
    const hits = searchSettings('font').map((h) => h.item)
    expect(hits.slice(0, 2)).toEqual(['fontUi', 'fontMono'])
  })

  it('finds placeholder sections and matches English while the UI is in another language', async () => {
    await setupApp('vi')
    expect(searchSettings('webhook')[0]).toMatchObject({ section: 'hooks', item: 'webhooks' })
    expect(searchSettings('chu de')[0]?.item).toBe('theme') // "Chủ đề màu", without diacritics
    expect(searchSettings('theme')[0]?.item).toBe('theme')
  })

  it.each([
    ['en', 'icon'],
    ['vi', 'biểu tượng'],
    ['vi', 'bieu tuong'],
    ['es', 'iconos'],
    ['ru', 'значки'],
    ['ja', 'アイコン'],
    ['ko', '아이콘'],
    ['zh-CN', '图标'],
  ] as const)('finds the file icon setting in %s with "%s"', async (locale, query) => {
    await setupApp(locale)
    expect(searchSettings(query)[0]).toMatchObject({ section: 'appearance', item: 'fileIcons' })
  })

  it('returns nothing for an empty query and no scattered-letter noise from descriptions', async () => {
    await setupApp('en')
    expect(searchSettings('  ')).toEqual([])
    expect(searchSettings('zzzq')).toEqual([])
  })

  it('lists every section item exactly once', () => {
    const items = SECTIONS.flatMap((s) => s.items)
    expect(new Set(items).size).toBe(items.length)
  })

  // The reverse of the check above (E-B25): a field the dialog shows but no section lists can never be found by
  // search. Every field is listed in the section that shows it; no exclusions are needed today.
  it('lists every field of FIELDS in the section that shows it', () => {
    for (const [section, defs] of Object.entries(FIELDS)) {
      expect(isFieldSection(section)).toBe(true)
      const info = SECTIONS.find((s) => s.id === section)
      expect(info, `section ${section}`).toBeDefined()
      for (const def of defs) {
        expect(info?.items, `${def.item} (${def.path})`).toContain(def.item)
      }
    }
  })

  it('finds the GitHub Enterprise hosts setting (E-B25)', async () => {
    await setupApp('en')
    expect(searchSettings('enterprise')[0]).toMatchObject({
      section: 'system',
      item: 'githubHosts',
    })
    expect(searchSettings('GitHub').map((h) => h.item)).toContain('githubHosts')
    expect(sectionInfo('system').items).toContain('githubHosts')
  })
})
