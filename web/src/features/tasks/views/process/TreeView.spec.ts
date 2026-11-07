import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import type { Turn } from '@/api/types'
import { useTasksStore } from '@/stores/tasks'
import { setupApp } from '@/test/app'
import { fakeTasksApi, iso, makeDetail, makeEvent, makeTask } from '@/test/fixtures'

import TreeView from './TreeView.vue'

const turn = (n: number): Turn =>
  ({ n, by: 'lead', message: `turn ${n}`, status: 'ok', startedAt: iso(), attempts: [] }) as never

describe('TreeView', () => {
  it('marks turns older than the loaded events as not loaded, never as 0 steps', async () => {
    const { plugins } = await setupApp()
    const store = useTasksStore()
    // Only turn 2's newest events are loaded (seq 301..305); turn 1 lies before them.
    const items = [301, 302, 303, 304, 305].map((seq) => ({ ...makeEvent('t1', seq), turn: 2 }))
    const events = vi.fn(() => Promise.resolve({ items, lastSeq: 305 }))
    store.setApi(fakeTasksApi({ events }))
    const task = makeTask({ id: 't1', lastEventSeq: 305 })
    store.upsert(task)
    const wrapper = mount(TreeView, {
      props: { task, detail: makeDetail({ id: 't1', turns: [turn(1), turn(2)] }) },
      global: { plugins },
    })
    await flushPromises()
    const heads = wrapper.findAll('.tree__head')
    expect(heads[0]?.text()).toContain('Not loaded')
    expect(heads[0]?.text()).not.toContain('0 steps')
    expect(heads[1]?.text()).toContain('0 steps')
    // The partial note and Collapse all share one row.
    const bar = wrapper.find('.pf__bar')
    expect(bar.text()).toContain('Only the newest steps are loaded.')
    expect(bar.text()).toContain('Collapse all')
    const load = wrapper
      .findAll('.tree__none button')
      .find((b) => b.text() === 'Load earlier steps')
    expect(load).toBeDefined()
    wrapper.unmount()
  })
})
