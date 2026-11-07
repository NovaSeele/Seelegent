import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { shallowRef } from 'vue'

import { flush, setupApp } from '@/test/app'
import { iso, makeTask } from '@/test/fixtures'

import FollowupComposer from './FollowupComposer.vue'
import type { TaskActions } from './useTaskActions'

const actions = () =>
  ({
    busy: shallowRef(null),
    followup: vi.fn(() => Promise.resolve(true)),
  }) as unknown as TaskActions

describe('FollowupComposer', () => {
  it('is one line until focused, then shows tier, model and the hint', async () => {
    const { plugins } = await setupApp()
    const wrapper = mount(FollowupComposer, {
      props: { task: makeTask({ status: 'done' }), actions: actions() },
      global: { plugins },
      attachTo: document.body,
    })
    expect(wrapper.find('textarea').attributes('rows')).toBe('1')
    expect(wrapper.findAll('select')).toHaveLength(0)
    await wrapper.find('textarea').trigger('focusin')
    await flush()
    expect(wrapper.find('textarea').attributes('rows')).toBe('3')
    expect(wrapper.findAll('select').length).toBeGreaterThan(0)
    wrapper.unmount()
  })

  it('says the worker waits for an answer instead of offering a disabled box', async () => {
    const { plugins } = await setupApp()
    const task = makeTask({
      status: 'running',
      attention: { state: 'question', reason: 'question', who: 'lead', since: iso() },
    })
    const wrapper = mount(FollowupComposer, {
      props: { task, actions: actions() },
      global: { plugins },
    })
    expect(wrapper.find('textarea').exists()).toBe(false)
    expect(wrapper.text()).toContain('waiting for your answer above')
    await wrapper.setProps({ task: { ...task, attention: null } })
    expect(wrapper.text()).toContain('The worker is busy')
  })
})
