import { mount, RouterLinkStub } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import { useTasksStore } from '@/stores/tasks'
import { setupApp } from '@/test/app'
import { iso, makeTask } from '@/test/fixtures'

import RunningCard from './RunningCard.vue'

async function mountCard(task: ReturnType<typeof makeTask>) {
  const { plugins } = await setupApp()
  useTasksStore().upsert(task)
  return mount(RunningCard, {
    props: { id: task.id, now: Date.now() },
    global: { plugins, stubs: { RouterLink: RouterLinkStub } },
  })
}

describe('RunningCard', () => {
  it('says how long a task waits for resources and how much memory it needs, with the settings', async () => {
    const wrapper = await mountCard(
      makeTask({
        id: 't1',
        status: 'waiting_quota',
        waiting: 'memory',
        attention: {
          state: 'info',
          reason: 'resources_wait',
          who: 'user',
          since: iso(),
          params: { minutes: 12, availableMB: 1536, needMB: 2048 },
        },
      }),
    )
    const line = wrapper.get('[data-resources]')
    expect(line.text()).toContain(
      'Waiting for resources for over 12 minutes: 1.5 GB free, needs 2 GB.',
    )
    const links = wrapper.findAllComponents(RouterLinkStub)
    expect(links.map((l) => l.props('to'))).toContainEqual({
      name: 'settings',
      params: { section: 'resources' },
    })
  })

  it('keeps the quota line for a task waiting on quota', async () => {
    const wrapper = await mountCard(
      makeTask({
        id: 't2',
        status: 'waiting_quota',
        waitingUntil: new Date(Date.now() + 24 * 60_000).toISOString(),
      }),
    )
    expect(wrapper.find('[data-resources]').exists()).toBe(false)
    expect(wrapper.text()).toContain('24 minutes')
  })

  it('names a wait for a free slot and the full accounts, not quota', async () => {
    const wrapper = await mountCard(
      makeTask({
        id: 't3',
        status: 'waiting_quota',
        waiting: 'busy',
        waitDetail: 'acc6: 2 of 2 running',
      }),
    )
    expect(wrapper.text()).toContain('Waiting for a free slot')
    expect(wrapper.text()).not.toContain('Waiting for quota')
    expect(wrapper.get('[data-slot]').text()).toBe('acc6: 2 of 2 running')
  })
})
