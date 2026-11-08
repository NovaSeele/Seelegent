/**
 * Locale-aware formatting on top of Intl (numbers, percentages, dates, relative times and durations).
 * Formatters are cached per locale; useFormat() follows the active vue-i18n locale reactively.
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

export type DateInput = Date | number | string

export interface Formatters {
  readonly locale: string
  number(value: number, options?: Intl.NumberFormatOptions): string
  /** 0.42 -> "42%". */
  percent(fraction: number, maximumFractionDigits?: number): string
  /** 12500 -> "12.5K". */
  compact(value: number): string
  date(value: DateInput, options?: Intl.DateTimeFormatOptions): string
  time(value: DateInput): string
  dateTime(value: DateInput): string
  /** "3 minutes ago", "in 2 hours", "now"; picks the largest unit that fits. */
  relative(value: DateInput, now?: number): string
  /**
   * A moment that is always past (a task's creation, a flag's start): relative, but `justNow` for one at or after
   * `now`. A page's clock ticks in steps, so a fresh moment can be ahead of it and must not read "in 11 seconds".
   */
  ago(value: DateInput, now: number | undefined, justNow: string): string
  /** 3_725_000 ms -> "1h 2m" (narrow units, at most two). */
  duration(ms: number): string
}

const UNITS: ReadonlyArray<[Intl.RelativeTimeFormatUnit, number]> = [
  ['year', 365 * 24 * 3600_000],
  ['month', 30 * 24 * 3600_000],
  ['week', 7 * 24 * 3600_000],
  ['day', 24 * 3600_000],
  ['hour', 3600_000],
  ['minute', 60_000],
  ['second', 1000],
]

const toTime = (value: DateInput): number =>
  value instanceof Date ? value.getTime() : typeof value === 'number' ? value : Date.parse(value)

const cache = new Map<string, Formatters>()

export function createFormatters(locale: string): Formatters {
  const cached = cache.get(locale)
  if (cached) return cached

  const numberCache = new Map<string, Intl.NumberFormat>()
  const num = (options: Intl.NumberFormatOptions = {}) => {
    const key = JSON.stringify(options)
    let format = numberCache.get(key)
    if (!format) {
      format = new Intl.NumberFormat(locale, options)
      numberCache.set(key, format)
    }
    return format
  }
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' })
  const dateFormat = new Intl.DateTimeFormat(locale, { dateStyle: 'medium' })
  const timeFormat = new Intl.DateTimeFormat(locale, { timeStyle: 'short' })
  const dateTimeFormat = new Intl.DateTimeFormat(locale, {
    dateStyle: 'medium',
    timeStyle: 'short',
  })
  const unit = (u: 'hour' | 'minute' | 'second' | 'day', n: number) =>
    num({ style: 'unit', unit: u, unitDisplay: 'narrow' }).format(n)

  const formatters: Formatters = {
    locale,
    number: (value, options) => num(options).format(value),
    percent: (fraction, digits = 0) =>
      num({ style: 'percent', maximumFractionDigits: digits }).format(fraction),
    compact: (value) => num({ notation: 'compact', maximumFractionDigits: 1 }).format(value),
    date: (value, options) =>
      (options ? new Intl.DateTimeFormat(locale, options) : dateFormat).format(toTime(value)),
    time: (value) => timeFormat.format(toTime(value)),
    dateTime: (value) => dateTimeFormat.format(toTime(value)),
    relative(value, now = Date.now()) {
      const diff = toTime(value) - now
      for (const [name, size] of UNITS) {
        if (Math.abs(diff) >= size || name === 'second') {
          return rtf.format(Math.round(diff / size), name)
        }
      }
      return rtf.format(0, 'second')
    },
    ago(value, now = Date.now(), justNow) {
      return toTime(value) >= now ? justNow : formatters.relative(value, now)
    },
    duration(ms) {
      const total = Math.max(0, Math.round(ms / 1000))
      const days = Math.floor(total / 86400)
      const hours = Math.floor((total % 86400) / 3600)
      const minutes = Math.floor((total % 3600) / 60)
      const seconds = total % 60
      const parts: string[] = []
      if (days) parts.push(unit('day', days))
      if (hours) parts.push(unit('hour', hours))
      if (minutes && parts.length < 2) parts.push(unit('minute', minutes))
      if (!days && !hours && (seconds || !minutes)) parts.push(unit('second', seconds))
      return parts.slice(0, 2).join(' ')
    },
  }
  cache.set(locale, formatters)
  return formatters
}

/** Formatters for the active UI language (reactive: re-evaluates when the locale changes). */
export function useFormat() {
  const { locale } = useI18n()
  return computed(() => createFormatters(locale.value))
}
