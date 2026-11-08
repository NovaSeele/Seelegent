/**
 * F21 (M14 live check, 375 px): a meeting's or contest's folder path in the page header widened the page, and the
 * 44 px note buttons of the diff view put their line number below the old one on touch screens. jsdom lays nothing
 * out, so these check the rules that keep both in place in the components' own styles.
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import { readFile } from '@/test/files'

import PageHeader from './PageHeader.vue'

/** The declarations of the first rule for selector in a component's styles, as a property map. */
function rule(path: string, selector: string): Record<string, string> {
  const css = readFile(path)
  const start = css.indexOf(`\n${selector} {`)
  expect(start, `${selector} in ${path}`).toBeGreaterThan(-1)
  const body = css.slice(css.indexOf('{', start) + 1, css.indexOf('}', start))
  const out: Record<string, string> = {}
  for (const decl of body.replace(/\/\*[\s\S]*?\*\//g, '').split(';')) {
    const [prop, ...value] = decl.split(':')
    if (prop?.trim() && value.length) out[prop.trim()] = value.join(':').trim()
  }
  return out
}

describe('long paths and note buttons (F21)', () => {
  it('puts the description in a text column that may shrink', () => {
    const w = mount(PageHeader, {
      props: { title: 'Meeting', description: 'C:/a/very/long/folder/path/without/any/spaces' },
    })
    expect(w.get('.page-header__text').find('.page-header__desc').exists()).toBe(true)
  })

  it('breaks a long description instead of widening the page', () => {
    const path = 'src/components/layout/PageHeader.vue'
    expect(rule(path, '.page-header__desc')['overflow-wrap']).toBe('anywhere')
    expect(rule(path, '.page-header__text')['min-width']).toBe('0')
  })

  // Since c932f7f a line number keeps the row's own height (so it stays level with the old one) and the 44 px
  // target is the separate "Add a note" button of the selected line.
  it('keeps line numbers at row height and puts the touch target on the add-note button', () => {
    const path = 'src/components/diff/DiffView.vue'
    expect(rule(path, '.diff__pick')['min-height']).toBeUndefined()
    expect(rule(path, '.diff__add')['min-height']).toBe('var(--target-min)')
  })
})
