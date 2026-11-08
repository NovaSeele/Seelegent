import { nextTick, onBeforeUnmount, ref, watch, type Ref } from 'vue'

export type Side = 'top' | 'bottom' | 'left' | 'right'
export type Align = 'start' | 'center' | 'end'
export type Placement = Side | `${Side}-${Exclude<Align, 'center'>}`

const VIEWPORT_MARGIN = 8

interface Rect {
  top: number
  left: number
  width: number
  height: number
}

function split(placement: Placement): [Side, Align] {
  const [side, align] = placement.split('-') as [Side, Align | undefined]
  return [side, align ?? 'center']
}

const opposite: Record<Side, Side> = { top: 'bottom', bottom: 'top', left: 'right', right: 'left' }

/**
 * Pure placement maths: where a floating box of `size` goes next to `anchor` inside a viewport, flipping to the
 * other side when it does not fit and clamping it inside the margins.
 */
export function computePosition(
  anchor: Rect,
  size: { width: number; height: number },
  viewport: { width: number; height: number },
  placement: Placement,
  offset: number,
): { top: number; left: number; side: Side } {
  const [preferred, align] = split(placement)
  let side = preferred
  const fits = (s: Side) => {
    if (s === 'bottom')
      return anchor.top + anchor.height + offset + size.height <= viewport.height - VIEWPORT_MARGIN
    if (s === 'top') return anchor.top - offset - size.height >= VIEWPORT_MARGIN
    if (s === 'right')
      return anchor.left + anchor.width + offset + size.width <= viewport.width - VIEWPORT_MARGIN
    return anchor.left - offset - size.width >= VIEWPORT_MARGIN
  }
  if (!fits(side) && fits(opposite[side])) side = opposite[side]

  let top: number
  let left: number
  if (side === 'top' || side === 'bottom') {
    top =
      side === 'bottom' ? anchor.top + anchor.height + offset : anchor.top - offset - size.height
    left =
      align === 'start'
        ? anchor.left
        : align === 'end'
          ? anchor.left + anchor.width - size.width
          : anchor.left + (anchor.width - size.width) / 2
  } else {
    left =
      side === 'right' ? anchor.left + anchor.width + offset : anchor.left - offset - size.width
    top =
      align === 'start'
        ? anchor.top
        : align === 'end'
          ? anchor.top + anchor.height - size.height
          : anchor.top + (anchor.height - size.height) / 2
  }
  const clamp = (value: number, max: number) =>
    Math.max(VIEWPORT_MARGIN, Math.min(value, max - VIEWPORT_MARGIN))
  left = clamp(left, viewport.width - size.width)
  top = clamp(top, viewport.height - size.height)
  return { top: Math.round(top), left: Math.round(left), side }
}

/** The style of a floating element that has no coordinates yet: laid out (so it can be measured) but unseen. */
const UNPLACED = { position: 'fixed', top: '0px', left: '0px', visibility: 'hidden' }

/**
 * Positions a teleported, fixed-position element next to its anchor while `open`, and keeps it there on scroll
 * and resize. Until it has coordinates for this opening the style keeps it `visibility: hidden`, so it never
 * flashes at the viewport's top-left corner or at a stale place. An anchor out of the document or not laid out
 * (a 0×0 box: its row re-rendered, its list switched layout) gives no coordinates: the element stays hidden and
 * `onLost` lets the owner close it (a tooltip does; a menu keeps its state and shows again once placed).
 */
export function useFloating(
  anchor: Ref<HTMLElement | null>,
  floating: Ref<HTMLElement | null>,
  open: Ref<boolean>,
  options: { placement?: Ref<Placement> | Placement; offset?: number; onLost?: () => void } = {},
) {
  const style = ref<Record<string, string>>({ ...UNPLACED })
  const side = ref<Side>('bottom')
  let observer: ResizeObserver | null = null

  function update() {
    const a = anchor.value
    const f = floating.value
    if (!a || !f) return
    const rect = a.getBoundingClientRect()
    if (!a.isConnected || (rect.width === 0 && rect.height === 0)) {
      style.value = { ...style.value, visibility: 'hidden' }
      options.onLost?.()
      return
    }
    const placement =
      typeof options.placement === 'string'
        ? options.placement
        : (options.placement?.value ?? 'bottom-start')
    const pos = computePosition(
      rect,
      { width: f.offsetWidth, height: f.offsetHeight },
      { width: window.innerWidth, height: window.innerHeight },
      placement,
      options.offset ?? 6,
    )
    side.value = pos.side
    style.value = { position: 'fixed', top: `${pos.top}px`, left: `${pos.left}px` }
  }

  function start() {
    window.addEventListener('resize', update)
    window.addEventListener('scroll', update, true)
    if (typeof ResizeObserver !== 'undefined' && floating.value) {
      observer = new ResizeObserver(update)
      observer.observe(floating.value)
    }
    update()
  }

  function stop() {
    window.removeEventListener('resize', update)
    window.removeEventListener('scroll', update, true)
    observer?.disconnect()
    observer = null
  }

  watch(
    open,
    async (isOpen) => {
      if (isOpen) {
        // Unseen until placed for this opening: the anchor may have moved since the last one. (Closing keeps the
        // place, so a leave transition plays where the element was.)
        style.value = { ...style.value, visibility: 'hidden' }
        await nextTick()
        start()
      } else stop()
    },
    { immediate: true },
  )

  onBeforeUnmount(stop)

  return { style, side, update }
}
