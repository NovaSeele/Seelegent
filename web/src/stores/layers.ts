/**
 * The overlay stack: dialogs, sheets, the palette, menus and popovers register here while open.
 * - Esc goes to the top layer only (one owner, no competing listeners).
 * - Global shortcuts are suspended while any layer is open.
 * - Page scroll is locked while a modal layer is open.
 */
import { defineStore } from 'pinia'
import { computed, shallowRef } from 'vue'

export type LayerKind = 'dialog' | 'sheet' | 'palette' | 'menu' | 'popover'

export interface LayerOptions {
  kind: LayerKind
  /** Modal layers lock page scroll and make the rest of the page inert for the focus trap. */
  modal: boolean
  /** Called when Esc is pressed while this layer is on top. */
  onEscape?: () => void
}

export interface Layer extends LayerOptions {
  readonly id: number
}

export const useLayersStore = defineStore('layers', () => {
  const stack = shallowRef<readonly Layer[]>([])
  let nextId = 1

  const top = computed(() => stack.value.at(-1))
  const anyOpen = computed(() => stack.value.length > 0)
  const modalOpen = computed(() => stack.value.some((layer) => layer.modal))

  function push(options: LayerOptions): number {
    const layer: Layer = { ...options, id: nextId++ }
    stack.value = [...stack.value, layer]
    syncScrollLock()
    return layer.id
  }

  function remove(id: number): void {
    stack.value = stack.value.filter((layer) => layer.id !== id)
    syncScrollLock()
  }

  function isTop(id: number): boolean {
    return top.value?.id === id
  }

  /** Sends Esc to the top layer; returns true when a layer took it. */
  function escape(): boolean {
    const layer = top.value
    if (!layer) return false
    layer.onEscape?.()
    return true
  }

  function syncScrollLock(): void {
    if (typeof document === 'undefined') return
    document.documentElement.toggleAttribute('data-scroll-locked', modalOpen.value)
  }

  return { stack, top, anyOpen, modalOpen, push, remove, isTop, escape }
})
