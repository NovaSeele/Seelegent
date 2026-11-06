import { computed, onBeforeUnmount, watch, type Ref } from 'vue'

import { useLayersStore, type LayerOptions } from '@/stores/layers'

/**
 * Registers the calling component as an overlay layer while `open` is true and tells whether it is the top
 * layer (the only one that reacts to Esc and traps focus).
 */
export function useLayer(open: Ref<boolean>, options: LayerOptions) {
  const layers = useLayersStore()
  let id: number | null = null

  watch(
    open,
    (isOpen) => {
      if (isOpen && id === null) id = layers.push(options)
      else if (!isOpen && id !== null) {
        layers.remove(id)
        id = null
      }
    },
    { immediate: true },
  )

  onBeforeUnmount(() => {
    if (id !== null) layers.remove(id)
    id = null
  })

  const isTop = computed(() => {
    const top = layers.top
    return id !== null && top?.id === id
  })

  return { isTop }
}
