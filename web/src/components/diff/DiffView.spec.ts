import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import type { FileDiff, Note } from '@/api/types'
import { flush, setupApp } from '@/test/app'

import { diffRows } from './diffRows'
import DiffView from './DiffView.vue'

const diff = (overrides: Partial<FileDiff> = {}): FileDiff => ({
  path: 'packages/web/pages/[id].vue',
  binary: false,
  truncated: false,
  hunks: [
    {
      header: '@@ -1,3 +1,3 @@',
      oldStart: 1,
      oldLines: 3,
      newStart: 1,
      newLines: 3,
      lines: [
        { kind: 'context', text: 'a', oldNo: 1, newNo: 1 },
        { kind: 'del', text: '\tb', oldNo: 2 },
        { kind: 'add', text: '\tB', newNo: 2 },
        { kind: 'context', text: 'c', oldNo: 3, newNo: 3 },
      ],
    },
    {
      header: '@@ -10 +10,2 @@ func x()',
      oldStart: 10,
      oldLines: 1,
      newStart: 10,
      newLines: 2,
      lines: [
        { kind: 'context', text: 'x', oldNo: 10, newNo: 10 },
        { kind: 'add', text: 'y', newNo: 11 },
      ],
    },
  ],
  ...overrides,
})

describe('diffRows', () => {
  it('puts a header row before each hunk and counts changes', () => {
    const model = diffRows(diff())
    expect(model.rows.map((r) => r.kind)).toEqual([
      'hunk',
      'context',
      'del',
      'add',
      'context',
      'hunk',
      'context',
      'add',
    ])
    expect(model).toMatchObject({ hidden: 0, additions: 2, deletions: 1 })
    expect(model.rows[2]).toMatchObject({ text: '\tb', oldNo: 2, newNo: undefined })
  })

  it('stops building rows past the limit but still counts everything', () => {
    const model = diffRows(diff(), 3)
    expect(model.rows.filter((r) => r.kind !== 'hunk')).toHaveLength(3)
    expect(model.rows.filter((r) => r.kind === 'hunk')).toHaveLength(1)
    expect(model).toMatchObject({ hidden: 3, additions: 2, deletions: 1 })
  })
})

describe('DiffView', () => {
  it('renders line numbers, signs named for screen readers, and keeps tabs', async () => {
    const { plugins } = await setupApp()
    const wrapper = mount(DiffView, { props: { diff: diff() }, global: { plugins } })
    const rows = wrapper.findAll('tr')
    expect(rows).toHaveLength(8)
    expect(rows[2]?.classes()).toContain('diff__row--del')
    expect(rows[2]?.find('.diff__text').text()).toBe('b')
    expect(rows[2]?.find('.diff__text').element.textContent).toBe('\tb')
    expect(rows[3]?.find('.sr-only').text()).toBe('added')
    expect(wrapper.find('caption').text()).toContain('2 added, 1 removed')
  })

  it('hides lines past the limit until asked', async () => {
    const { plugins } = await setupApp()
    const wrapper = mount(DiffView, { props: { diff: diff(), limit: 2 }, global: { plugins } })
    expect(wrapper.text()).toContain('4 more lines hidden.')
    await wrapper.find('.diff__note--more button').trigger('click')
    await flush()
    expect(wrapper.findAll('tr')).toHaveLength(8)
    expect(wrapper.find('.diff__note--more').exists()).toBe(false)
  })

  it('says so for binary and truncated diffs', async () => {
    const { plugins } = await setupApp()
    const binary = mount(DiffView, {
      props: { diff: diff({ binary: true, hunks: [] }) },
      global: { plugins },
    })
    expect(binary.text()).toContain('Binary file')
    const cut = mount(DiffView, { props: { diff: diff({ truncated: true }) }, global: { plugins } })
    expect(cut.text()).toContain('cut short')
  })

  it('offers new-side line numbers for notes and shows the notes under their lines', async () => {
    const { plugins } = await setupApp()
    const note: Note = {
      id: 'n1',
      taskId: 't1',
      file: 'packages/web/pages/[id].vue',
      line: 2,
      currentLine: 11,
      side: 'new',
      anchorText: 'y',
      body: 'Rename y',
      by: 'user',
      state: 'open',
      status: 'draft',
      anchorTurn: 1,
      createdAt: '2026-11-07T10:00:00.000Z',
    }
    const plain = mount(DiffView, { props: { diff: diff() }, global: { plugins } })
    expect(plain.find('.diff__pick').exists()).toBe(false)
    const wrapper = mount(DiffView, {
      props: { diff: diff(), annotatable: true, notes: [note] },
      global: { plugins },
    })
    const picks = wrapper.findAll('.diff__pick')
    // Every row with a new-side number: the deleted line has none.
    expect(picks.map((b) => b.text())).toEqual(['1', '2', '3', '10', '11'])
    expect(picks[1]?.attributes('aria-label')).toBe('Line 2')
    // One roving tab stop: the first line number.
    expect(picks.map((b) => b.attributes('tabindex'))).toEqual(['0', '-1', '-1', '-1', '-1'])
    // A tap selects the line and shows one add button under it; the button asks for the note.
    await picks[1]?.trigger('click')
    expect(picks[1]?.attributes('aria-pressed')).toBe('true')
    const add = wrapper.findAll('button').filter((b) => b.text() === 'Add a note on line 2')
    expect(add).toHaveLength(1)
    await add[0]?.trigger('click')
    expect(wrapper.emitted('line')).toEqual([[2]])
    // The note follows its anchor to line 11, the last row.
    const rows = wrapper.findAll('tr')
    expect(rows.at(-1)?.classes()).toContain('extra-row')
    expect(rows.at(-1)?.text()).toBe('Rename y')
  })

  it('moves between line numbers with the arrows and opens the composer under its line', async () => {
    const { plugins } = await setupApp()
    const wrapper = mount(DiffView, {
      props: { diff: diff(), annotatable: true },
      slots: { composer: '<textarea class="compose"></textarea>' },
      global: { plugins },
      attachTo: document.body,
    })
    const picks = () => wrapper.findAll('.diff__pick')
    await picks()[0]?.trigger('keydown', { key: 'ArrowDown' })
    await flush()
    expect(document.activeElement?.textContent?.trim()).toBe('2')
    expect(picks().map((b) => b.attributes('tabindex'))).toEqual(['-1', '0', '-1', '-1', '-1'])
    await picks()[1]?.trigger('keydown', { key: 'End' })
    await flush()
    expect(document.activeElement?.textContent?.trim()).toBe('11')
    // The composer of line 3 sits right under line 3, in place of the add button.
    await wrapper.setProps({ composerLine: 3 })
    const rows = wrapper.findAll('tr')
    const at = rows.findIndex((r) => r.find('.diff__pick').exists() && r.text().includes('3'))
    expect(rows[at + 1]?.find('.compose').exists()).toBe(true)
    // When it closes, the focus is back on line 3.
    await wrapper.setProps({ composerLine: null })
    await flush()
    expect(document.activeElement?.textContent?.trim()).toBe('3')
    wrapper.unmount()
  })

  it('does not anchor a note whose line is gone', async () => {
    const { plugins } = await setupApp()
    const gone = {
      id: 'n2',
      taskId: 't1',
      file: 'x',
      line: 2,
      currentLine: 0,
      side: 'new',
      anchorText: 'y',
      body: 'Gone',
      by: 'user',
      state: 'sent',
      status: 'addressed',
      anchorTurn: 1,
      createdAt: '2026-11-07T10:00:00.000Z',
    } as Note
    const wrapper = mount(DiffView, {
      props: { diff: diff(), annotatable: true, notes: [gone] },
      global: { plugins },
    })
    expect(wrapper.text()).not.toContain('Gone')
  })
})
