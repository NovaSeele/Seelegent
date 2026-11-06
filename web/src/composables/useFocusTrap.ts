import { nextTick, onBeforeUnmount, watch, type Ref } from 'vue'

const FOCUSABLE = [
  'a[href]',
  'area[href]',
  'button:not([disabled])',
  'input:not([disabled]):not([type="hidden"])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  'iframe',
  '[contenteditable="true"]',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

/** Tabbable elements inside `root`, in DOM order, skipping hidden ones. */
export function focusableIn(root: HTMLElement): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>(FOCUSABLE)].filter(
    (el) => !el.hasAttribute('inert') && !el.closest('[inert]') && el.getClientRects().length > 0,
  )
}

/** Like focusableIn, but without the layout check (jsdom has no layout). */
function candidates(root: HTMLElement): HTMLElement[] {
  const visible = focusableIn(root)
  return visible.length > 0 ? visible : [...root.querySelectorAll<HTMLElement>(FOCUSABLE)]
}

export interface FocusTrapOptions {
  /** Element to focus first; defaults to the first tabbable element, else the container. */
  initialFocus?: () => HTMLElement | null | undefined
  /** Whether this trap is the active one (top of the layer stack). */
  enabled?: Ref<boolean>
}

/**
 * Keeps Tab / Shift+Tab inside `container` while `active`, focuses the first element when it activates and
 * returns focus to whatever had it before when it deactivates.
 */
export function useFocusTrap(
  container: Ref<HTMLElement | null>,
  active: Ref<boolean>,
  options: FocusTrapOptions = {},
) {
  let previous: HTMLElement | null = null

  function onKeydown(event: KeyboardEvent) {
    if (event.key !== 'Tab' || !container.value) return
    if (options.enabled && !options.enabled.value) return
    const items = candidates(container.value)
    if (items.length === 0) {
      event.preventDefault()
      container.value.focus()
      return
    }
    const first = items[0]
    const last = items[items.length - 1]
    const current = document.activeElement
    if (event.shiftKey && (current === first || !container.value.contains(current))) {
      event.preventDefault()
      last?.focus()
    } else if (!event.shiftKey && (current === last || !container.value.contains(current))) {
      event.preventDefault()
      first?.focus()
    }
  }

  async function activate() {
    previous = document.activeElement instanceof HTMLElement ? document.activeElement : null
    document.addEventListener('keydown', onKeydown, true)
    await nextTick()
    const root = container.value
    if (!root) return
    const target = options.initialFocus?.() ?? candidates(root)[0] ?? root
    target.focus({ preventScroll: true })
  }

  function deactivate() {
    document.removeEventListener('keydown', onKeydown, true)
    const target = previous
    previous = null
    if (target && target.isConnected) target.focus({ preventScroll: true })
  }

  watch(
    active,
    (isActive) => {
      if (isActive) void activate()
      else deactivate()
    },
    { immediate: true },
  )

  onBeforeUnmount(() => {
    if (active.value) deactivate()
  })
}
