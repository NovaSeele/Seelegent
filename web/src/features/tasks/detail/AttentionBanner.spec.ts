import { mount, RouterLinkStub } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { shallowRef } from 'vue'

import { flush, setupApp } from '@/test/app'
import { iso, makeTask } from '@/test/fixtures'

import AttentionBanner from './AttentionBanner.vue'
import type { TaskActions } from './useTaskActions'

const actions = () =>
  ({
    busy: shallowRef(null),
    answer: vi.fn(() => Promise.resolve(true)),
    dismiss: vi.fn(() => Promise.resolve(true)),
  }) as unknown as TaskActions

const question = {
  state: 'question',
  reason: 'question',
  who: 'lead',
  since: iso(),
  detail: 'Which redirect should the old URL get?',
  params: { options: ['301', '302'] as string[] },
} as const

describe('AttentionBanner', () => {
  it.each(['running', 'done', 'failed'] as const)(
    'shows the question and its answer card on a %s task',
    async (status) => {
      const { plugins } = await setupApp()
      const a = actions()
      const wrapper = mount(AttentionBanner, {
        props: { task: makeTask({ status, attention: question }), actions: a },
        global: { plugins },
      })
      await flush()
      expect(wrapper.text()).toContain('Which redirect should the old URL get?')
      expect(wrapper.find('textarea').exists()).toBe(true)
      const option = wrapper.findAll('button').find((b) => b.text().includes('301'))
      expect(option).toBeDefined()
      await option?.trigger('click')
      expect(a.answer).toHaveBeenCalledWith('301')
    },
  )

  it('shows a resources_wait flag with its amounts and the Resources settings, but no other info flag', async () => {
    const { plugins } = await setupApp()
    const wait = {
      state: 'info',
      reason: 'resources_wait',
      who: 'user',
      since: iso(),
      params: { minutes: 12, availableMB: 1536, needMB: 2048 },
    } as const
    const wrapper = mount(AttentionBanner, {
      props: {
        task: makeTask({ status: 'waiting_quota', waiting: 'memory', attention: wait }),
        actions: actions(),
      },
      global: { plugins, stubs: { RouterLink: RouterLinkStub } },
    })
    await flush()
    expect(wrapper.text()).toContain('Waiting for resources')
    expect(wrapper.text()).toContain('over 12 minutes: 1.5 GB free, needs 2 GB.')
    expect(wrapper.text()).not.toContain('Needs you')
    expect(wrapper.get('section').classes()).toContain('tone-neutral')
    expect(wrapper.getComponent(RouterLinkStub).props('to')).toEqual({
      name: 'settings',
      params: { section: 'resources' },
    })

    const note = {
      state: 'info',
      reason: 'hygiene',
      who: 'lead',
      since: iso(),
      params: { count: 2 },
    } as const
    await wrapper.setProps({ task: makeTask({ status: 'running', attention: note }) })
    expect(wrapper.find('section').exists()).toBe(false)
  })
})
