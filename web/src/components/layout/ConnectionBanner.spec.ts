import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'

import { useConnectionStore } from '@/stores/connection'
import { setupApp } from '@/test/app'

import ConnectionBanner from './ConnectionBanner.vue'

function setVisibility(state: DocumentVisibilityState) {
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => state })
  document.dispatchEvent(new Event('visibilitychange'))
}

describe('ConnectionBanner timer (E-B24)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    setVisibility('visible')
  })
  afterEach(() => {
    vi.useRealTimers()
    setVisibility('visible')
  })

  async function mounted() {
    const { plugins } = await setupApp()
    const wrapper = mount(ConnectionBanner, { global: { plugins } })
    return { wrapper, connection: useConnectionStore() }
  }

  it('runs no timer while online', async () => {
    const { connection } = await mounted()
    connection.status = 'online'
    await nextTick()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('counts down only while offline with a retry ahead, then stops', async () => {
    const { wrapper, connection } = await mounted()
    connection.status = 'offline'
    connection.retryAt = Date.now() + 3000
    await nextTick()
    expect(vi.getTimerCount()).toBe(1)
    expect(wrapper.text()).toContain('3')

    await vi.advanceTimersByTimeAsync(1000)
    expect(wrapper.find('.banner__count').text()).toContain('2')

    await vi.advanceTimersByTimeAsync(2500)
    expect(vi.getTimerCount()).toBe(0)

    connection.status = 'online'
    connection.retryAt = null
    await nextTick()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('stops while the tab is hidden and resumes when it is shown', async () => {
    const { connection } = await mounted()
    connection.status = 'offline'
    connection.retryAt = Date.now() + 60_000
    await nextTick()
    expect(vi.getTimerCount()).toBe(1)
    setVisibility('hidden')
    await nextTick()
    expect(vi.getTimerCount()).toBe(0)
    setVisibility('visible')
    await nextTick()
    expect(vi.getTimerCount()).toBe(1)
  })

  it('clears its timer when unmounted', async () => {
    const { wrapper, connection } = await mounted()
    connection.status = 'offline'
    connection.retryAt = Date.now() + 60_000
    await nextTick()
    wrapper.unmount()
    expect(vi.getTimerCount()).toBe(0)
  })
})
