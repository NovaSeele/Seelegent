/**
 * A virtual list for long feeds whose items differ in height (the Terminal transcript): only the items near the
 * viewport are in the DOM, padded above and below to the full height. Rendered items are measured with a
 * ResizeObserver. While `following`, the list stays pinned to the end as items arrive or grow; otherwise the
 * first visible item keeps its place when items load above it or change height (manual scroll anchoring, so the
 * container sets `overflow-anchor: none`).
 */
import { computed, nextTick, onBeforeUnmount, onMounted, shallowRef, watch, type Ref } from 'vue'

import { fromBottom, layout, visibleRange } from '@/lib/virtual'

/** Within this many px of the end counts as "at the end" (the user is following). */
export const FOLLOW_THRESHOLD = 48

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

  function onScroll() {
    const el = scroller.value
    if (!el) return
    top.value = el.scrollTop
    following.value = fromBottom(el) <= FOLLOW_THRESHOLD
    rememberAnchor()
  }

  function scrollToEnd() {
    const el = scroller.value
    if (!el) return
    following.value = true
    el.scrollTop = el.scrollHeight
    top.value = el.scrollTop
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
      } else if (anchor) {
        const index = keys.value.indexOf(anchor.key)
        if (index >= 0) {
          const target = (offsets.value[index] ?? 0) + anchor.delta
          if (Math.abs(target - el.scrollTop) > 1) el.scrollTop = target
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
          if (following.value) el.scrollTop = el.scrollHeight
        })

  onMounted(() => {
    const el = scroller.value
    if (!el) return
    viewport.value = el.clientHeight
    el.addEventListener('scroll', onScroll, { passive: true })
    viewportObserver?.observe(el)
    scrollToEnd()
  })

  onBeforeUnmount(() => {
    scroller.value?.removeEventListener('scroll', onScroll)
    observer?.disconnect()
    viewportObserver?.disconnect()
    if (frame) cancelAnimationFrame(frame)
  })

  return { range, visible, padTop, padBottom, following, measure, scrollToEnd }
}
