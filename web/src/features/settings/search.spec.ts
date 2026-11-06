import { describe, expect, it } from 'vitest'

import { setupApp } from '@/test/app'

import { searchSettings } from './search'
import { SECTIONS } from './sections'

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

  it('returns nothing for an empty query and no scattered-letter noise from descriptions', async () => {
    await setupApp('en')
    expect(searchSettings('  ')).toEqual([])
    expect(searchSettings('zzzq')).toEqual([])
  })

  it('lists every section item exactly once', () => {
    const items = SECTIONS.flatMap((s) => s.items)
    expect(new Set(items).size).toBe(items.length)
  })
})
