import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import Icon from '@/components/ui/Icon.vue'
import { setupApp } from '@/test/app'

import HookCard from './HookCard.vue'

describe('HookCard', () => {
  it('marks the chosen events with a check, not by colour alone', async () => {
    const { plugins } = await setupApp()
    const wrapper = mount(HookCard, {
      props: {
        modelValue: { id: 'h1', on: ['task.done'], run: 'echo', timeoutSeconds: 20, enabled: true },
        index: 0,
        lastRun: null,
        errors: {},
      },
      global: { plugins },
    })
    const chips = wrapper.findAll('button[aria-pressed]')
    const done = chips.find((c) => c.text() === 'task.done')
    const failed = chips.find((c) => c.text() === 'task.failed')
    expect(done?.attributes('aria-pressed')).toBe('true')
    expect(done?.findComponent(Icon).props('name')).toBe('check')
    expect(failed?.findComponent(Icon).exists()).toBe(false)
  })
})
