/**
 * A virtual list for long feeds whose items differ in height (the Terminal transcript): only the items near the
 * viewport are in the DOM, padded above and below to the full height. Rendered items are measured with a
 * ResizeObserver. While `following`, the list stays pinned to the end as items arrive or grow; otherwise the
 * first visible item keeps its place when items load above it or change height (manual scroll anchoring, so the
 * container sets `overflow-anchor: none`). Following stops only on the reader's own input: wheel up, touch, the
 * up keys (PageUp, ArrowUp, Home, Shift+Space), a held primary or middle button anywhere in the list (scrollbar,
 * drag-selecting text, middle-click autoscroll) and focus moving inside it (Tab/Shift+Tab scrolling an item in).
 * A scroll the layout causes (items measured shorter or taller than estimated) re-pins the end.
 * Not detected, so re-pinned while following: Ctrl+F find-in-page and screen-reader browse mode (an accepted limit
 * of a virtual list).
 */
import { computed, nextTick, onBeforeUnmount, onMounted, shallowRef, watch, type Ref } from 'vue'

import { fromBottom, keepsFollowing, layout, visibleRange } from '@/lib/virtual'

/** Within this many px of the end counts as "at the end" (the user is following). */
export const FOLLOW_THRESHOLD = 48
/** A scroll this soon after the reader's input is theirs (wheel and key scrolls arrive a frame or two later). */
export const READER_INPUT_MS = 600
const UP_KEYS = new Set(['PageUp', 'ArrowUp', 'Home'])

export interface VirtualListOptions {
  scroller: Readonly<Ref<HTMLElement | null>>
  keys: Readonly<Ref<readonly string[]>>
  estimate: number
  overscan?: number
}

export function useVirtualList(options: VirtualListOptions) {
  const { scroller, keys, estimate } = options
  const overscan = options.overscan ?? 600
  const heights = shallowRef(new Map<string, number>())
  const top = shallowRef(0)
  const viewport = shallowRef(0)
  const following = shallowRef(true)
  /** scrollTop after the last scroll event or programmatic scroll: a smaller one means the reader went up. */
  let lastTop = 0
  let anchor: { key: string; delta: number } | null = null

  const offsets = computed(() => layout(keys.value, heights.value, estimate))
  const range = computed(() => visibleRange(offsets.value, top.value, viewport.value, overscan))
  const padTop = computed(() => offsets.value[range.value.start] ?? 0)
  const padBottom = computed(() => {
    const all = offsets.value
    return (all[all.length - 1] ?? 0) - (all[range.value.end] ?? 0)
  })
  const visible = computed(() => keys.value.slice(range.value.start, range.value.end))

  function rememberAnchor() {
    const el = scroller.value
    if (!el) return
    const index = Math.min(keys.value.length - 1, range.value.start)
    let i = Math.max(0, index)
    const all = offsets.value
    while (i < keys.value.length - 1 && (all[i + 1] ?? 0) <= el.scrollTop) i++
    const key = keys.value[i]
    anchor = key === undefined ? null : { key, delta: el.scrollTop - (all[i] ?? 0) }
  }

  // The reader's own scrolling: only this may stop following.
  let inputAt = -Infinity
  /** A primary or middle button is held down inside the list. */
  let pressing = false
  /** That press was on the scroller itself (its scrollbar or padding), not on an item. */
  let pressOnBar = false
  const readerInput = () => pressing || Date.now() - inputAt < READER_INPUT_MS
  const markInput = () => (inputAt = Date.now())
  const onWheel = (event: WheelEvent) => event.deltaY < 0 && markInput()
  const onKeydown = (event: KeyboardEvent) => {
    if (UP_KEYS.has(event.key) || (event.key === ' ' && event.shiftKey)) markInput()
  }
  /**
   * A primary or middle press anywhere in the list counts as reader input until it is released: dragging the
   * scrollbar (classic or overlay, which sits inside the client box), drag-selecting text past the edge (the
   * browser autoscrolls) or middle-button autoscroll.
   */
  const onPointerdown = (event: PointerEvent) => {
    if (event.button !== 0 && event.button !== 1) return
    pressing = true
    pressOnBar = event.target === scroller.value
  }
  /** pointercancel (a touch pan) and window blur also end a press, so the flag can never stick. */
  const releasePress = () => {
    pressing = false
    pressOnBar = false
  }
  /**
   * The grace period after release is kept only for a press on the scroller itself (a scrollbar can still be
   * scrolling); a plain click on an item, such as a ToolItem toggle, does not count once released.
   */
  const onPointerup = () => {
    if (pressing && pressOnBar) markInput()
    releasePress()
  }

  function onScroll() {
    const el = scroller.value
    if (!el) return
    following.value = keepsFollowing(following.value, lastTop, el, FOLLOW_THRESHOLD, readerInput())
    // Still following but off the end: the layout moved it (a clamp after re-measuring), so go back.
    if (following.value && fromBottom(el) > FOLLOW_THRESHOLD) el.scrollTop = el.scrollHeight
    lastTop = el.scrollTop
    top.value = el.scrollTop
    rememberAnchor()
  }

  function scrollToEnd() {
    const el = scroller.value
    if (!el) return
    following.value = true
    el.scrollTop = el.scrollHeight
    lastTop = el.scrollTop
    top.value = el.scrollTop
  }

  /** Scrolls so the item starts near the top; false when the key is not in the list. */
  function scrollToKey(key: string): boolean {
    const el = scroller.value
    const index = keys.value.indexOf(key)
    if (!el || index < 0) return false
    following.value = false
    el.scrollTop = Math.max(0, (offsets.value[index] ?? 0) - 48)
    lastTop = el.scrollTop
    top.value = el.scrollTop
    rememberAnchor()
    return true
  }

  // After the layout changes (items added, measured, loaded above), keep the end or the anchor in view.
  watch(
    offsets,
    async () => {
      await nextTick()
      const el = scroller.value
      if (!el) return
      if (following.value) {
        el.scrollTop = el.scrollHeight
        lastTop = el.scrollTop
      } else if (anchor) {
        const index = keys.value.indexOf(anchor.key)
        if (index >= 0) {
          const target = (offsets.value[index] ?? 0) + anchor.delta
          if (Math.abs(target - el.scrollTop) > 1) el.scrollTop = target
          lastTop = el.scrollTop
        }
      }
      top.value = el.scrollTop
    },
    { flush: 'post' },
  )

  // Measuring: heights are batched into one update per frame.
  const pending = new Map<string, number>()
  let frame = 0
  const elementKeys = new WeakMap<Element, string>()
  function onResize(entries: readonly ResizeObserverEntry[], self: ResizeObserver) {
    for (const entry of entries) {
      // An item scrolled out of the window was removed; its last height stays valid.
      if (!entry.target.isConnected) {
        self.unobserve(entry.target)
        continue
      }
      const key = elementKeys.get(entry.target)
      if (key === undefined) continue
      const size = entry.borderBoxSize?.[0]?.blockSize ?? entry.contentRect.height
      if (heights.value.get(key) !== size) pending.set(key, size)
    }
    if (pending.size && !frame) frame = requestAnimationFrame(flush)
  }
  const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(onResize)

  function flush() {
    frame = 0
    if (!pending.size) return
    const next = new Map(heights.value)
    for (const [key, size] of pending) next.set(key, size)
    pending.clear()
    // Forget heights of items that left the list.
    if (next.size > keys.value.length * 2) {
      const live = new Set(keys.value)
      for (const key of next.keys()) if (!live.has(key)) next.delete(key)
    }
    heights.value = next
  }

  /** Ref callback for each rendered item. */
  function measure(el: unknown, key: string) {
    if (!(el instanceof Element) || !observer) return
    if (elementKeys.get(el) === key) return
    elementKeys.set(el, key)
    observer.observe(el)
  }

  const viewportObserver =
    typeof ResizeObserver === 'undefined'
      ? null
      : new ResizeObserver(() => {
          const el = scroller.value
          if (!el) return
          viewport.value = el.clientHeight
          if (following.value) {
            el.scrollTop = el.scrollHeight
            lastTop = el.scrollTop
          }
        })

  onMounted(() => {
    const el = scroller.value
    if (!el) return
    viewport.value = el.clientHeight
    el.addEventListener('scroll', onScroll, { passive: true })
    el.addEventListener('wheel', onWheel, { passive: true })
    el.addEventListener('touchmove', markInput, { passive: true })
    el.addEventListener('keydown', onKeydown)
    el.addEventListener('pointerdown', onPointerdown)
    // Focus moving inside the list (Tab, Shift+Tab) scrolls the focused item into view.
    el.addEventListener('focusin', markInput)
    window.addEventListener('pointerup', onPointerup)
    window.addEventListener('pointercancel', releasePress)
    window.addEventListener('blur', releasePress)
    viewportObserver?.observe(el)
    scrollToEnd()
  })

  onBeforeUnmount(() => {
    const el = scroller.value
    el?.removeEventListener('scroll', onScroll)
    el?.removeEventListener('wheel', onWheel)
    el?.removeEventListener('touchmove', markInput)
    el?.removeEventListener('keydown', onKeydown)
    el?.removeEventListener('pointerdown', onPointerdown)
    el?.removeEventListener('focusin', markInput)
    window.removeEventListener('pointerup', onPointerup)
    window.removeEventListener('pointercancel', releasePress)
    window.removeEventListener('blur', releasePress)
    observer?.disconnect()
    viewportObserver?.disconnect()
    if (frame) cancelAnimationFrame(frame)
  })

  return { range, visible, padTop, padBottom, following, measure, scrollToEnd, scrollToKey }
}
