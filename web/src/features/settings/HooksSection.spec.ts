import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/api/client'
import type { SettingsApi } from '@/api/settings'
import { useSettingsStore } from '@/stores/settings'
import { setupApp } from '@/test/app'

import HooksSection from './HooksSection.vue'

async function mountSection(hooks: SettingsApi['hooks']) {
  const { plugins } = await setupApp()
  useSettingsStore().setApi({ hooks } as unknown as SettingsApi)
  const wrapper = mount(HooksSection, { global: { plugins } })
  await flushPromises()
  return wrapper
}

const buttons = (wrapper: Awaited<ReturnType<typeof mountSection>>) =>
  wrapper.findAll('button').map((b) => b.text())

describe('HooksSection', () => {
  it('says a daemon without hooks (501) does not have them yet, with nothing to retry or add', async () => {
    const wrapper = await mountSection(
      vi.fn(() => Promise.reject(new ApiError(501, 'not_implemented', 'no hooks'))),
    )
    expect(wrapper.text()).toContain('does not support hooks yet')
    expect(buttons(wrapper)).not.toContain('Retry')
    expect(buttons(wrapper)).not.toContain('Add hook')
  })

  it('offers Retry when the hooks cannot be read for another reason', async () => {
    const wrapper = await mountSection(
      vi.fn(() => Promise.reject(new ApiError(500, 'internal', 'disk full'))),
    )
    expect(wrapper.text()).toContain('Hooks could not be loaded: disk full')
    expect(buttons(wrapper)).toContain('Retry')
  })

  it('lists the hooks and offers Add when the daemon has them', async () => {
    const wrapper = await mountSection(vi.fn(() => Promise.resolve([])))
    expect(wrapper.text()).toContain('No hooks yet.')
    expect(buttons(wrapper)).toContain('Add hook')
  })
})
