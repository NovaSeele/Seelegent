import { nextTick, type Ref } from 'vue'

/**
 * Arrow-key navigation for a row of options with one tab stop (tabs, radio-like segmented controls).
 * Left/Right (and Up/Down) move with wrap-around, Home/End jump to the ends; the moved-to option is selected.
 */
export function useRovingFocus<T>(
  options: Ref<readonly { value: T; disabled?: boolean }[]>,
  select: (value: T) => void,
  container: Ref<HTMLElement | null>,
) {
  async function focusValue(index: number) {
    const option = options.value[index]
    if (!option) return
    select(option.value)
    await nextTick()
    container.value?.querySelectorAll<HTMLElement>('[data-roving]')[index]?.focus()
  }

  function onKeydown(event: KeyboardEvent, current: number) {
    const list = options.value
    const enabled = list.map((o, i) => (o.disabled ? -1 : i)).filter((i) => i >= 0)
    if (enabled.length === 0) return
    const pos = Math.max(0, enabled.indexOf(current))
    let next: number | undefined
    const rtl = container.value ? getComputedStyle(container.value).direction === 'rtl' : false
    switch (event.key) {
      case 'ArrowRight':
        next = enabled[(pos + (rtl ? -1 : 1) + enabled.length) % enabled.length]
        break
      case 'ArrowDown':
        next = enabled[(pos + 1) % enabled.length]
        break
      case 'ArrowLeft':
        next = enabled[(pos + (rtl ? 1 : -1) + enabled.length) % enabled.length]
        break
      case 'ArrowUp':
        next = enabled[(pos - 1 + enabled.length) % enabled.length]
        break
      case 'Home':
        next = enabled[0]
        break
      case 'End':
        next = enabled[enabled.length - 1]
        break
      default:
        return
    }
    event.preventDefault()
    if (next !== undefined) void focusValue(next)
  }

  return { onKeydown }
}
