/**
 * The one breakpoint set of the dashboard (pairs with the media queries in CSS, see styles/tokens.css):
 *   mobile   width < 600px          top app bar + bottom nav
 *   compact  600px <= width < 900px  icon rail with tooltips
 *   rail     width >= 900px          rail with icon + short label, side-by-side panes
 * CSS uses range syntax with exactly these numbers, `(width < 600px)` / `(width >= 900px)`, so there is no
 * off-by-one between CSS and script. A unit test rejects any other width in a media query.
 *
 * One height breakpoint: below 760px tall (a 1366×768 laptop minus browser chrome is about 680px) the rail
 * tightens its items so all eleven pages fit without scrolling, `(height < 760px)`.
 */
export const BREAKPOINTS = {
  compact: 600,
  rail: 900,
} as const

export const HEIGHT_BREAKPOINTS = {
  short: 760,
} as const

export type LayoutTier = 'mobile' | 'compact' | 'rail'

export const MEDIA = {
  compactUp: `(width >= ${BREAKPOINTS.compact}px)`,
  railUp: `(width >= ${BREAKPOINTS.rail}px)`,
} as const

export function tierForWidth(width: number): LayoutTier {
  if (width >= BREAKPOINTS.rail) return 'rail'
  if (width >= BREAKPOINTS.compact) return 'compact'
  return 'mobile'
}
