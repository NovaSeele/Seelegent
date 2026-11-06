import type { Ref } from 'vue'

import type { LayerKind } from '@/stores/layers'

import { useFocusTrap } from './useFocusTrap'
import { useLayer } from './useLayer'

/**
 * Shared behaviour of modal surfaces (Dialog, Sheet, palette): a layer on the stack (Esc while on top, shortcut
 * suspension, scroll lock), a focus trap active while on top, and focus returned to the opener on close.
 */
export function useOverlay(
  open: Ref<boolean>,
  panel: Ref<HTMLElement | null>,
  options: {
    kind: LayerKind
    onClose: () => void
    initialFocus?: () => HTMLElement | null | undefined
  },
) {
  const { isTop } = useLayer(open, { kind: options.kind, modal: true, onEscape: options.onClose })
  useFocusTrap(panel, open, { initialFocus: options.initialFocus, enabled: isTop })
  return { isTop }
}
