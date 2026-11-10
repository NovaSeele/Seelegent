import { mount, RouterLinkStub } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import { needKind } from '@/lib/taskStatus'
import { useTasksStore } from '@/stores/tasks'
import { setupApp } from '@/test/app'
import { iso, makeTask } from '@/test/fixtures'

import NeedCard from './NeedCard.vue'

async function mountCard(task: ReturnType<typeof makeTask>) {
  const { plugins } = await setupApp()
  useTasksStore().upsert(task)
  const kind = needKind(task)
  if (!kind) throw new Error('the task needs no one')
  return mount(NeedCard, {
    props: { id: task.id, kind, since: iso(-60_000), now: Date.now() },
    global: { plugins, stubs: { RouterLink: RouterLinkStub } },
  })
}

describe('NeedCard', () => {
  it('shows a finished task with a red gate as needing action, with its failed gate, not as a review', async () => {
    const wrapper = await mountCard(
      makeTask({
        id: 'red',
        status: 'done',
        review: null,
        gate: { state: 'red', autoFixes: 2 },
        attention: { state: 'blocked', reason: 'gate_red', who: 'lead', since: iso(-60_000) },
      }),
    )
    expect(wrapper.get('[data-need]').attributes('data-need')).toBe('blocked')
    expect(wrapper.text()).toContain('Needs action')
    expect(wrapper.text()).toContain('Gate failed')
    expect(wrapper.text()).not.toContain('Awaiting review')
    const links = wrapper.findAllComponents(RouterLinkStub).map((l) => l.props('to'))
    expect(links).not.toContainEqual(expect.objectContaining({ query: { view: 'git' } }))
  })

  it('keeps a finished task with a passing gate under awaiting review', async () => {
    const wrapper = await mountCard(
      makeTask({
        id: 'green',
        status: 'done',
        review: null,
        gate: { state: 'green', autoFixes: 0 },
        attention: { state: 'info', reason: 'review', who: 'lead', since: iso(-60_000) },
      }),
    )
    expect(wrapper.get('[data-need]').attributes('data-need')).toBe('review')
    expect(wrapper.text()).toContain('Awaiting review')
  })
})
