import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick, ref } from 'vue'

import { READER_INPUT_MS, useVirtualList } from './useVirtualList'

/** ResizeObserver stand-in: the test reports sizes itself. */
class FakeResizeObserver {
  static all: FakeResizeObserver[] = []
  targets = new Set<Element>()
  constructor(readonly callback: ResizeObserverCallback) {
    FakeResizeObserver.all.push(this)
  }
  observe(el: Element) {
    this.targets.add(el)
  }
  unobserve(el: Element) {
    this.targets.delete(el)
  }
  disconnect() {
    this.targets.clear()
  }
  report(size: (el: Element) => number) {
    const entries = [...this.targets].map(
      (target) =>
        ({
          target,
          borderBoxSize: [{ blockSize: size(target) }],
        }) as unknown as ResizeObserverEntry,
    )
    this.callback(entries, this as unknown as ResizeObserver)
  }
}

const ESTIMATE = 100
const VIEWPORT = 500
const FRAME_MS = 333 // a hidden pane: about three frames a second

/**
 * A scroller with a layout the test controls: content height is padTop + rendered items + padBottom, rendered
 * items have their real (DOM) height, and scrollTop is clamped to the content like a browser does.
 */
function setup() {
  const keys = ref(Array.from({ length: 100 }, (_, i) => `k${i}`))
  const real = new Map<string, number>()
  let api!: ReturnType<typeof useVirtualList>
  let stored = 0
  const contentHeight = () =>
    api.padTop.value +
    api.padBottom.value +
    api.visible.value.reduce((sum, key) => sum + (real.get(key) ?? ESTIMATE), 0)
  const max = () => Math.max(0, contentHeight() - VIEWPORT)

  const Host = defineComponent({
    setup() {
      const scroller = ref<HTMLElement | null>(null)
      api = useVirtualList({ scroller, keys, estimate: ESTIMATE })
      return () =>
        h(
          'div',
          {
            ref: (el) => {
              const div = el as HTMLElement | null
              if (div && !scroller.value) {
                Object.defineProperties(div, {
                  clientHeight: { get: () => VIEWPORT },
                  scrollHeight: { get: () => contentHeight() },
                  scrollTop: {
                    get: () => Math.min(stored, max()),
                    set: (v: number) => (stored = Math.max(0, Math.min(v, max()))),
                  },
                })
              }
              scroller.value = div
            },
          },
          api.visible.value.map((key) =>
            h('div', { key, 'data-key': key, ref: (el) => api.measure(el, key) }),
          ),
        )
    },
  })
  const wrapper = mount(Host, { attachTo: document.body })
  const el = wrapper.element as HTMLElement
  const fromBottom = () => el.scrollHeight - el.scrollTop - el.clientHeight
  /** The browser lays out with the real heights; the observer reports them on its own clock. */
  const measureShorter = (height: number) => {
    for (const key of api.visible.value) real.set(key, height)
    for (const observer of FakeResizeObserver.all) observer.report(() => height)
  }
  /** A rendered item, the target of the reader's clicks and focus. */
  const item = () => el.querySelector('[data-key]') as HTMLElement
  /** The reader (or the browser on their behalf) scrolls up. */
  const scrollUp = (by: number) => {
    el.scrollTop = el.scrollTop - by
    el.dispatchEvent(new Event('scroll'))
  }
  /** The layout moves scrollTop up with no input from the reader. */
  const layoutScroll = () => {
    stored = Math.max(0, el.scrollTop - 300)
    el.dispatchEvent(new Event('scroll'))
  }
  return {
    api,
    el,
    wrapper,
    fromBottom,
    measureShorter,
    item,
    scrollUp,
    layoutScroll,
    setStored: (v: number) => (stored = v),
  }
}

describe('useVirtualList following', () => {
  beforeEach(() => {
    FakeResizeObserver.all = []
    vi.useFakeTimers()
    vi.stubGlobal('ResizeObserver', FakeResizeObserver)
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) =>
      setTimeout(() => cb(Date.now()), FRAME_MS),
    )
    vi.stubGlobal('cancelAnimationFrame', (id: number) => clearTimeout(id))
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  it('stays at the bottom when items measure shorter than the estimate, on a slow frame clock', async () => {
    const { api, el, wrapper, fromBottom, measureShorter, setStored } = setup()
    await nextTick()
    expect(fromBottom()).toBe(0)
    expect(api.following.value).toBe(true)
    const before = el.scrollTop
    for (let round = 0; round < 6; round++) {
      // Items render shorter than estimated: the content shrinks under the reader and the browser moves
      // scrollTop down before any frame lets the list re-measure (no input from the reader).
      measureShorter(40)
      setStored(Math.max(0, el.scrollTop - 700))
      el.dispatchEvent(new Event('scroll'))
      await vi.advanceTimersByTimeAsync(FRAME_MS * 2)
      await nextTick()
      expect(api.following.value).toBe(true)
      expect(fromBottom()).toBeLessThanOrEqual(1)
    }
    expect(el.scrollTop).toBeLessThan(before)
    wrapper.unmount()
  })

  it('stops following when the reader wheels up, and not before', async () => {
    const { api, el, wrapper, fromBottom } = setup()
    await nextTick()
    el.dispatchEvent(new WheelEvent('wheel', { deltaY: 120 }))
    el.scrollTop = el.scrollTop - 400
    el.dispatchEvent(new Event('scroll'))
    // A downward wheel is no reason to stop: the list snaps back to the end.
    expect(api.following.value).toBe(true)
    expect(fromBottom()).toBe(0)

    el.dispatchEvent(new WheelEvent('wheel', { deltaY: -120 }))
    el.scrollTop = el.scrollTop - 400
    el.dispatchEvent(new Event('scroll'))
    expect(api.following.value).toBe(false)
    expect(fromBottom()).toBe(400)
    // Later layout changes keep the reader's place.
    await vi.advanceTimersByTimeAsync(FRAME_MS * 3)
    expect(api.following.value).toBe(false)
    wrapper.unmount()
  })
  it('keeps the place of a reader who drag-selects text upward from an item', async () => {
    const { api, wrapper, fromBottom, item, scrollUp } = setup()
    await nextTick()
    item().dispatchEvent(new PointerEvent('pointerdown', { button: 0, bubbles: true }))
    // The browser autoscrolls the selection up, long after any grace period.
    await vi.advanceTimersByTimeAsync(READER_INPUT_MS * 2)
    scrollUp(400)
    expect(api.following.value).toBe(false)
    expect(fromBottom()).toBe(400)
    await vi.advanceTimersByTimeAsync(FRAME_MS * 3)
    expect(api.following.value).toBe(false)
    expect(fromBottom()).toBe(400)
    window.dispatchEvent(new PointerEvent('pointerup', { button: 0 }))
    wrapper.unmount()
  })

  it('keeps the place of a reader whose Shift+Tab focuses an item above the viewport', async () => {
    const { api, wrapper, fromBottom, item, scrollUp } = setup()
    await nextTick()
    item().dispatchEvent(
      new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true }),
    )
    item().dispatchEvent(new FocusEvent('focusin', { bubbles: true }))
    scrollUp(400)
    expect(api.following.value).toBe(false)
    expect(fromBottom()).toBe(400)
    wrapper.unmount()
  })

  it('does not let a cancelled press or a blurred window stick as reader input', async () => {
    const { api, wrapper, fromBottom, item, layoutScroll } = setup()
    await nextTick()
    item().dispatchEvent(new PointerEvent('pointerdown', { button: 0, bubbles: true }))
    // A touch pan ends in pointercancel, never pointerup.
    window.dispatchEvent(new PointerEvent('pointercancel'))
    await vi.advanceTimersByTimeAsync(READER_INPUT_MS)
    layoutScroll()
    expect(api.following.value).toBe(true)
    expect(fromBottom()).toBe(0)

    item().dispatchEvent(new PointerEvent('pointerdown', { button: 1, bubbles: true }))
    window.dispatchEvent(new Event('blur'))
    await vi.advanceTimersByTimeAsync(READER_INPUT_MS)
    layoutScroll()
    expect(api.following.value).toBe(true)
    expect(fromBottom()).toBe(0)
    wrapper.unmount()
  })

  it('forgets a plain click on an item once it is released', async () => {
    const { api, wrapper, fromBottom, item, layoutScroll } = setup()
    await nextTick()
    item().dispatchEvent(new PointerEvent('pointerdown', { button: 0, bubbles: true }))
    // A real browser focuses the clicked control on press.
    item().dispatchEvent(new FocusEvent('focusin', { bubbles: true }))
    window.dispatchEvent(new PointerEvent('pointerup', { button: 0 }))
    layoutScroll()
    expect(api.following.value).toBe(true)
    expect(fromBottom()).toBe(0)
    wrapper.unmount()
  })

  it('still stops following on PageUp', async () => {
    const { api, el, wrapper, fromBottom, scrollUp } = setup()
    await nextTick()
    el.dispatchEvent(new KeyboardEvent('keydown', { key: 'PageUp', bubbles: true }))
    scrollUp(400)
    expect(api.following.value).toBe(false)
    expect(fromBottom()).toBe(400)
    wrapper.unmount()
  })
})
