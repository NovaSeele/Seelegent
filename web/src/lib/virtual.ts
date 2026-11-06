/**
 * Layout math of a virtual list with items of different heights: prefix offsets from measured heights (an
 * estimate for items not measured yet), and the range of items that overlaps the viewport plus an overscan margin.
 */

/** offsets[i] is the top of item i; offsets[n] is the total height. */
export function layout(
  keys: readonly string[],
  heights: ReadonlyMap<string, number>,
  estimate: number,
): number[] {
  const offsets = new Array<number>(keys.length + 1)
  let top = 0
  offsets[0] = 0
  keys.forEach((key, i) => {
    top += heights.get(key) ?? estimate
    offsets[i + 1] = top
  })
  return offsets
}

/** The last index whose top is at or above `y` (0 for an empty list). */
export function indexAt(offsets: readonly number[], y: number): number {
  let lo = 0
  let hi = Math.max(0, offsets.length - 2)
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1
    if ((offsets[mid] ?? 0) <= y) lo = mid
    else hi = mid - 1
  }
  return lo
}

export interface Range {
  start: number
  /** Exclusive. */
  end: number
}

/** Items that overlap [top - overscan, top + viewport + overscan]. */
export function visibleRange(
  offsets: readonly number[],
  top: number,
  viewport: number,
  overscan: number,
): Range {
  const count = offsets.length - 1
  if (count <= 0) return { start: 0, end: 0 }
  const start = indexAt(offsets, Math.max(0, top - overscan))
  const bottom = top + viewport + overscan
  let end = indexAt(offsets, bottom) + 1
  end = Math.min(count, Math.max(end, start + 1))
  return { start, end }
}

/** Distance in px between the bottom of the viewport and the end of the content. */
export const fromBottom = (el: { scrollTop: number; scrollHeight: number; clientHeight: number }) =>
  el.scrollHeight - el.scrollTop - el.clientHeight

/**
 * Whether a feed keeps following its end after a scroll event. Only the reader scrolling up stops following:
 * content that grows after a programmatic jump to the end (the scroll event arrives a frame later, when items
 * have been measured taller) must not, or a running task opens mid-way with "Jump to latest" showing.
 */
export function keepsFollowing(
  following: boolean,
  previousTop: number,
  el: { scrollTop: number; scrollHeight: number; clientHeight: number },
  threshold: number,
): boolean {
  if (fromBottom(el) <= threshold) return true
  if (el.scrollTop < previousTop - 1) return false
  return following
}
