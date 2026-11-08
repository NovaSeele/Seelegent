import { mount, RouterLinkStub } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import { useTasksStore } from '@/stores/tasks'
import { setupApp } from '@/test/app'
import { makeTask } from '@/test/fixtures'

import DepLinks from './DepLinks.vue'

describe('DepLinks', () => {
  it('names an earlier task by its title, with its id as secondary text (design review)', async () => {
    const { plugins } = await setupApp()
    useTasksStore().upsert(makeTask({ id: 't-early', title: 'Migrate the cart to TypeScript.' }))
    const wrapper = mount(DepLinks, {
      props: { ids: ['t-early', 't-gone'] },
      global: { plugins, stubs: { RouterLink: RouterLinkStub } },
    })
    const links = wrapper.findAllComponents(RouterLinkStub)
    expect(links).toHaveLength(2)
    expect(links[0]?.find('.deps__title').text()).toBe('Migrate the cart to TypeScript.')
    expect(links[0]?.find('.deps__id').text()).toBe('t-early')
    expect(links[0]?.attributes('title')).toBe('t-early')
    expect(links[0]?.props('to')).toEqual({ name: 'tasks', params: { taskId: 't-early' } })
    // A task that is not loaded keeps its id.
    expect(links[1]?.text()).toBe('t-gone')
    expect(links[1]?.find('.deps__title').exists()).toBe(false)
  })
})
