import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/api/client'
import type { SettingsApi } from '@/api/settings'
import type { Settings } from '@/api/types'
import { useConnectionStore } from '@/stores/connection'
import { useSettingsStore } from '@/stores/settings'
import { setupApp } from '@/test/app'

import DaemonSection from './DaemonSection.vue'

const SETTINGS = {
  port: 47831,
  maxConcurrentPerAccount: 2,
  loopRepeat: 6,
  linkIntoProfiles: ['.agents/skills'],
} as unknown as Settings

async function mountSection(section: 'workers' | 'system', patch: SettingsApi['patch']) {
  const { plugins } = await setupApp()
  useConnectionStore().settings = SETTINGS
  useSettingsStore().setApi({ patch } as unknown as SettingsApi)
  return mount(DaemonSection, { props: { section }, global: { plugins }, attachTo: document.body })
}

describe('DaemonSection', () => {
  it('saves a number as a one-field merge patch when the field is left', async () => {
    const patch = vi.fn<SettingsApi['patch']>((body) =>
      Promise.resolve({ ...SETTINGS, ...body } as Settings),
    )
    const wrapper = await mountSection('workers', patch)
    const input = wrapper.get('[data-setting="concurrency"] input')
    await input.setValue('4')
    await input.trigger('blur')
    await flushPromises()
    expect(patch).toHaveBeenCalledWith({ maxConcurrentPerAccount: 4 })
    expect(useConnectionStore().settings?.maxConcurrentPerAccount).toBe(4)
    expect(wrapper.get('[data-setting="concurrency"]').text()).toContain('Saved')
    wrapper.unmount()
  })

  it('rounds a whole-number setting and sends it once for Enter then blur', async () => {
    const patch = vi.fn<SettingsApi['patch']>((body) =>
      Promise.resolve({ ...SETTINGS, ...body } as Settings),
    )
    const wrapper = await mountSection('workers', patch)
    const input = wrapper.get('[data-setting="concurrency"] input')
    await input.setValue('4.6')
    await input.trigger('keydown', { key: 'Enter' })
    await flushPromises()
    await input.trigger('blur')
    await flushPromises()
    expect(patch).toHaveBeenCalledTimes(1)
    expect(patch).toHaveBeenCalledWith({ maxConcurrentPerAccount: 5 })
    expect((input.element as HTMLInputElement).value).toBe('5')
    wrapper.unmount()
  })

  it('says which values a field takes for one out of its range, without sending it', async () => {
    const patch = vi.fn<SettingsApi['patch']>()
    const wrapper = await mountSection('workers', patch)
    const input = wrapper.get('[data-setting="loopRepeat"] input')
    await input.setValue('1')
    await input.trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(wrapper.get('[data-setting="loopRepeat"]').text()).toContain(
      'Enter a whole number from 2 to 100.',
    )
    expect(patch).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('shows the daemon’s message for a value it refuses, and keeps the old value', async () => {
    const patch = vi.fn<SettingsApi['patch']>(() =>
      Promise.reject(
        new ApiError(400, 'invalid_argument', 'invalid', {
          fields: [{ field: 'loopRepeat', message: 'not while tasks run' }],
        }),
      ),
    )
    const wrapper = await mountSection('workers', patch)
    const input = wrapper.get('[data-setting="loopRepeat"] input')
    await input.setValue('9')
    await input.trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(wrapper.get('[data-setting="loopRepeat"]').text()).toContain('not while tasks run')
    expect(useConnectionStore().settings?.loopRepeat).toBe(6)
    wrapper.unmount()
  })

  it('clears the error, red border and aria-invalid once the saved value is back', async () => {
    const patch = vi.fn<SettingsApi['patch']>()
    const wrapper = await mountSection('workers', patch)
    const row = () => wrapper.get('[data-setting="loopRepeat"]')
    const input = row().get('input')
    await input.setValue('500')
    await input.trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(input.attributes('aria-invalid')).toBe('true')
    expect(row().find('.num--invalid').exists()).toBe(true)
    await input.setValue('6')
    await input.trigger('blur')
    await flushPromises()
    expect(input.attributes('aria-invalid')).toBeUndefined()
    expect(row().find('.num--invalid').exists()).toBe(false)
    expect(row().text()).not.toContain('Enter a whole number')
    // Esc back to the saved value clears it too.
    await input.setValue('500')
    await input.trigger('keydown', { key: 'Enter' })
    await flushPromises()
    await input.setValue('7')
    await input.trigger('keydown', { key: 'Escape' })
    await flushPromises()
    expect(input.attributes('aria-invalid')).toBeUndefined()
    expect(patch).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('resets a setting to its default with a null', async () => {
    const patch = vi.fn<SettingsApi['patch']>(() => Promise.resolve(SETTINGS))
    const wrapper = await mountSection('workers', patch)
    const reset = wrapper
      .get('[data-setting="loopRepeat"]')
      .findAll('button')
      .find((b) => b.attributes('aria-label')?.startsWith('Reset'))
    expect(reset).toBeDefined()
    await reset?.trigger('click')
    await flushPromises()
    expect(patch).toHaveBeenCalledWith({ loopRepeat: null })
    wrapper.unmount()
  })

  it('shows the port without a control to change it', async () => {
    const patch = vi.fn<SettingsApi['patch']>()
    const wrapper = await mountSection('system', patch)
    const row = wrapper.get('[data-setting="port"]')
    expect(row.text()).toContain('47831')
    expect(row.find('input').exists()).toBe(false)
    wrapper.unmount()
  })
})
