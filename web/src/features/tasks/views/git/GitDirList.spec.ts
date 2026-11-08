import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import type { GitDir, GitFile } from '@/api/types'
import { setupApp } from '@/test/app'

import GitDirList from './GitDirList.vue'

const untracked = (path: string): GitFile => ({
  path,
  status: 'untracked',
  additions: 1,
  deletions: 0,
  binary: false,
})

const dir: GitDir = {
  path: '.scratch/',
  count: 432,
  bytes: 3 * 1024 * 1024,
  files: [untracked('.scratch/a.json'), untracked('.scratch/b.json')],
}

describe('GitDirList', () => {
  it('shows a folder as one closed row that opens to its files and the count of the rest', async () => {
    const { plugins } = await setupApp()
    const wrapper = mount(GitDirList, {
      props: {
        dirs: [dir],
        selected: null,
        expanded: new Set<string>(),
        'onUpdate:expanded': (value: ReadonlySet<string>) => wrapper.setProps({ expanded: value }),
        'onUpdate:selected': (value: string | null) => wrapper.setProps({ selected: value }),
      },
      global: { plugins },
    })
    const row = wrapper.get('button')
    expect(row.attributes('aria-expanded')).toBe('false')
    expect(row.text()).toContain('.scratch/')
    expect(row.text()).toContain('432 files')
    expect(row.text()).toContain('3 MB')
    expect(wrapper.find('[role="listbox"]').exists()).toBe(false)

    await row.trigger('click')
    expect(row.attributes('aria-expanded')).toBe('true')
    expect(wrapper.findAll('[role="option"]')).toHaveLength(2)
    expect(wrapper.text()).toContain('and 430 more files')
    await wrapper.findAll('[role="option"]')[1]?.trigger('click')
    expect(wrapper.props('selected')).toBe('.scratch/b.json')

    await row.trigger('click')
    expect(wrapper.find('[role="listbox"]').exists()).toBe(false)
  })
})
