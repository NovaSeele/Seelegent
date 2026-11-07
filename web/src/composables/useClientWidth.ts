/** The inner width of an element, kept current with a ResizeObserver (null before it is measured). */
import { onBeforeUnmount, ref, type Ref, watch } from 'vue'

export function useClientWidth(el: Ref<HTMLElement | null>): Ref<number | null> {
  const width = ref<number | null>(null)
  let observer: ResizeObserver | null = null
  watch(el, (node) => {
    observer?.disconnect()
    if (!node || typeof ResizeObserver === 'undefined') return
    observer = new ResizeObserver(() => (width.value = node.clientWidth))
    observer.observe(node)
  })
  onBeforeUnmount(() => observer?.disconnect())
  return width
}
