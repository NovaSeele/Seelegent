import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  defaultDuration,
  DURATION_LONG,
  DURATION_PLAIN,
  MAX_TOASTS,
  useToastStore,
} from '@/stores/toast'
import { flush, setupApp } from '@/test/app'

import ToastContainer from './ToastContainer.vue'

describe('toast store', () => {
  beforeEach(async () => {
    await setupApp()
  })

  it('keeps at most MAX_TOASTS, dropping the oldest', () => {
    const store = useToastStore()
    for (let i = 0; i < MAX_TOASTS + 2; i++) store.show({ message: `m${i}` })
    expect(store.toasts.map((t) => t.message)).toEqual(['m2', 'm3', 'm4', 'm5'])
  })

  it('gives toasts with actions and errors more time', () => {
    expect(defaultDuration({ message: 'x' })).toBe(DURATION_PLAIN)
    expect(defaultDuration({ message: 'x', tone: 'error' })).toBe(DURATION_LONG)
    expect(defaultDuration({ message: 'x', actions: [{ label: 'a', run: () => {} }] })).toBe(
      DURATION_LONG,
    )
    expect(defaultDuration({ message: 'x', duration: 0 })).toBe(0)
  })
})

describe('ToastContainer', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  async function setup() {
    const { plugins } = await setupApp()
    vi.useFakeTimers()
    const wrapper = mount(ToastContainer, { attachTo: document.body, global: { plugins } })
    return { wrapper, store: useToastStore() }
  }

  const toasts = () => [...document.querySelectorAll('.toast')]

  it('shows a toast in a labelled region and dismisses it after its duration', async () => {
    const { wrapper, store } = await setup()
    store.show({ message: 'Saved' })
    await flush()
    expect(document.querySelector('section.toasts')?.getAttribute('aria-label')).toBe(
      'Notifications',
    )
    expect(toasts()).toHaveLength(1)
    expect(toasts()[0]?.getAttribute('role')).toBe('status')
    vi.advanceTimersByTime(DURATION_PLAIN + 10)
    await flush()
    expect(store.toasts).toHaveLength(0)
    wrapper.unmount()
  })

  it('announces errors as alerts and pauses while hovered', async () => {
    const { wrapper, store } = await setup()
    store.show({ message: 'Failed', tone: 'error' })
    await flush()
    const el = toasts()[0] as HTMLElement
    expect(el.getAttribute('role')).toBe('alert')
    el.dispatchEvent(new Event('pointerenter'))
    vi.advanceTimersByTime(DURATION_LONG * 2)
    await flush()
    expect(store.toasts).toHaveLength(1)
    el.dispatchEvent(new Event('pointerleave'))
    vi.advanceTimersByTime(DURATION_LONG)
    await flush()
    expect(store.toasts).toHaveLength(0)
    wrapper.unmount()
  })

  it('runs an action and closes the toast', async () => {
    const { wrapper, store } = await setup()
    const run = vi.fn()
    store.show({ message: 'Reset', actions: [{ label: 'Undo', run }] })
    await flush()
    const action = [...document.querySelectorAll<HTMLButtonElement>('.toast button')].find(
      (b) => b.textContent?.trim() === 'Undo',
    )
    action?.click()
    await flush()
    expect(run).toHaveBeenCalledOnce()
    expect(store.toasts).toHaveLength(0)
    wrapper.unmount()
  })

  it('closes with its close button', async () => {
    const { wrapper, store } = await setup()
    store.show({ message: 'Hello', duration: 0 })
    await flush()
    document.querySelector<HTMLButtonElement>('[aria-label="Dismiss notification"]')?.click()
    await flush()
    expect(store.toasts).toHaveLength(0)
    wrapper.unmount()
  })
})
