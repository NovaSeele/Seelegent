/**
 * Pure helpers of the Usage page: change against the previous period, chart scales, and the quota series of one
 * account from GET /usage/history (hourly rows or daily rollups).
 */
import type { History, Totals } from '@/api/types'

/** Relative change from `previous` to `current` (0.25 = +25 %); null when there is nothing to compare with. */
export function change(current: number, previous: number): number | null {
  if (previous === 0) return current === 0 ? 0 : null
  return (current - previous) / previous
}

export type TotalKey = 'tokens' | 'costUsd' | 'tasks' | 'turns'

export const TOTAL_KEYS: readonly TotalKey[] = ['tokens', 'costUsd', 'tasks', 'turns']

export function totalsChange(current: Totals, previous: Totals, key: TotalKey): number | null {
  return change(current[key], previous[key])
}

/** A round axis maximum at or above `value` (1, 2, 2.5, 5 × 10ⁿ); 1 for an empty chart. */
export function niceMax(value: number): number {
  if (!(value > 0)) return 1
  const power = 10 ** Math.floor(Math.log10(value))
  for (const step of [1, 2, 2.5, 5, 10]) if (step * power >= value) return step * power
  return 10 * power
}

export interface Point {
  /** ms since epoch */
  t: number
  /** 0..1 left */
  v: number
}

export interface QuotaSeries {
  /** "<group> · <window>" */
  key: string
  points: Point[]
  /** The lowest remaining in the range. */
  low: number
}

export interface HistoryAccount {
  id: string
  label: string
  backend: string
}

/** The accounts the history has rows for, in first-seen order. */
export function historyAccounts(history: History | null): HistoryAccount[] {
  const seen = new Map<string, HistoryAccount>()
  for (const row of [...(history?.rows ?? []), ...(history?.days ?? [])]) {
    if (!seen.has(row.accountId)) {
      seen.set(row.accountId, { id: row.accountId, label: row.label, backend: row.backend })
    }
  }
  return [...seen.values()]
}

/**
 * One line per quota window of `accountId`, oldest first. Hourly rows give the remaining at each hour; daily
 * rollups give the lowest remaining of each day (at local noon, so the day's point sits mid-day).
 */
export function quotaSeries(history: History | null, accountId: string): QuotaSeries[] {
  const lines = new Map<string, Point[]>()
  const add = (key: string, point: Point) => {
    const list = lines.get(key) ?? []
    list.push(point)
    lines.set(key, list)
  }
  for (const row of history?.rows ?? []) {
    if (row.accountId !== accountId) continue
    const t = Date.parse(row.at)
    for (const w of row.quota) add(`${w.group} · ${w.label || w.window}`, { t, v: w.remaining })
  }
  for (const day of history?.days ?? []) {
    if (day.accountId !== accountId) continue
    const [y, m, d] = day.date.split('-').map(Number)
    const t = new Date(y ?? 1970, (m ?? 1) - 1, d ?? 1, 12).getTime()
    for (const w of day.quota) {
      add(`${w.group} · ${w.label || w.window}`, { t, v: w.minRemaining ?? w.remaining })
    }
  }
  return [...lines.entries()].map(([key, points]) => {
    points.sort((a, b) => a.t - b.t)
    return { key, points, low: Math.min(...points.map((p) => p.v)) }
  })
}

/** An SVG path through `points` scaled into a width × height box over [t0, t1] × [0, 1]. */
export function linePath(
  points: readonly Point[],
  t0: number,
  t1: number,
  width: number,
  height: number,
): string {
  const span = Math.max(1, t1 - t0)
  return points
    .map((p, i) => {
      const x = ((p.t - t0) / span) * width
      const y = (1 - p.v) * height
      return `${i ? 'L' : 'M'}${x.toFixed(1)} ${y.toFixed(1)}`
    })
    .join(' ')
}
