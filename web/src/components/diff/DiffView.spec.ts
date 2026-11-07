import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import type { FileDiff } from '@/api/types'
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
})
