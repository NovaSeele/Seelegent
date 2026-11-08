import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import type { ProvidersApi } from '@/api/providers'
import { useProvidersStore } from '@/stores/providers'
import { setupApp } from '@/test/app'
import { makeProvider, makeTask } from '@/test/fixtures'

import TaskHeader from './TaskHeader.vue'
import type { TaskActions } from './useTaskActions'

describe('TaskHeader', () => {
  it("names an API-key task's provider when the task is the first page opened", async () => {
    // A fresh store: no other page (Providers, Add account) has read the providers yet.
    const { plugins } = await setupApp()
    const list = vi.fn(() =>
      Promise.resolve({ items: [makeProvider({ id: 'deepseek', name: 'DeepSeek' })], presets: [] }),
    )
    useProvidersStore().setApi({ list } as unknown as ProvidersApi)

    const wrapper = mount(TaskHeader, {
      props: {
        task: makeTask({ backend: 'claude', provider: 'deepseek' }),
        titleLevel: 1,
        showBack: false,
        actions: {} as TaskActions,
      },
      global: {
        plugins,
        stubs: { TaskHeaderActions: true, DepLinks: true, RouterLink: RouterLinkStub },
      },
    })
    await flushPromises()
    expect(list).toHaveBeenCalledTimes(1)
    expect(wrapper.find('.pm__chip').text()).toBe('DeepSeek')
  })
})
