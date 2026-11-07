/**
 * The dashboard's reading of a 5-field cron expression, the same rules as the daemon's parser
 * (internal/automation/cron.go): it says what is wrong with an expression under the field as the user types, says
 * when one can never run, and describes the common shapes in words. The daemon checks everything again on save.
 */

export type CronFieldName = 'minute' | 'hour' | 'dom' | 'month' | 'dow'

export interface CronField {
  /** The field starts with "*" ("*", "*\/5"). */
  star: boolean
  /** "*\/n" alone: the step n; else null. */
  every: number | null
  /** The values the field matches, ascending (Sunday is 0). */
  values: number[]
}

export interface Cron {
  minute: CronField
  hour: CronField
  dom: CronField
  month: CronField
  dow: CronField
  /** Vixie cron's rule: when the day-of-month or the day-of-week field starts with "*", a day must match both. */
  dayAnd: boolean
}

export type CronProblem =
  | { kind: 'count'; n: number }
  | { kind: 'value' | 'part' | 'step'; field: CronFieldName; value: string }
  | { kind: 'range'; field: CronFieldName; value: string; min: number; max: number }

interface Spec {
  name: CronFieldName
  min: number
  max: number
  names?: readonly string[]
  base?: number
}

const MONTHS = ['jan', 'feb', 'mar', 'apr', 'may', 'jun', 'jul', 'aug', 'sep', 'oct', 'nov', 'dec']
const DAYS = ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat']
const SPECS: readonly Spec[] = [
  { name: 'minute', min: 0, max: 59 },
  { name: 'hour', min: 0, max: 23 },
  { name: 'dom', min: 1, max: 31 },
  { name: 'month', min: 1, max: 12, names: MONTHS, base: 1 },
  { name: 'dow', min: 0, max: 7, names: DAYS, base: 0 },
]
const DIGITS = /^\d+$/

function value(spec: Spec, s: string): number | null {
  if (DIGITS.test(s)) return Number(s)
  const i = spec.names?.indexOf(s) ?? -1
  return i >= 0 ? i + (spec.base ?? 0) : null
}

function parseField(spec: Spec, text: string): CronField | CronProblem {
  const field = spec.name
  const set = new Set<number>()
  for (const part of text.split(',')) {
    const [rng = '', stepText, extra] = part.split('/')
    if (part === '' || extra !== undefined) return { kind: 'part', field, value: part }
    let step = 1
    if (stepText !== undefined) {
      if (!DIGITS.test(stepText) || Number(stepText) < 1)
        return { kind: 'step', field, value: stepText }
      step = Number(stepText)
    }
    let lo: number
    let hi: number
    if (rng === '*') {
      lo = spec.min
      hi = field === 'dow' ? 6 : spec.max
    } else if (rng.includes('-')) {
      const [a = '', b = '', more] = rng.split('-')
      if (more !== undefined) return { kind: 'part', field, value: rng }
      const va = value(spec, a)
      if (va === null) return { kind: 'value', field, value: a }
      const vb = value(spec, b)
      if (vb === null) return { kind: 'value', field, value: b }
      lo = va
      hi = vb
    } else {
      const v = value(spec, rng)
      if (v === null) return { kind: 'value', field, value: rng }
      lo = v
      hi = stepText !== undefined ? spec.max : v
    }
    if (lo < spec.min || hi > spec.max || lo > hi)
      return { kind: 'range', field, value: rng, min: spec.min, max: spec.max }
    for (let v = lo; v <= hi; v += step) set.add(v)
  }
  if (field === 'dow' && set.delete(7)) set.add(0)
  const star = text.startsWith('*')
  const m = /^\*\/(\d+)$/.exec(text)
  return {
    star,
    every: m ? Number(m[1]) : null,
    values: [...set].sort((a, b) => a - b),
  }
}

/** Parses `expr`, or says the first thing wrong with it. */
export function parseCron(expr: string): { cron: Cron } | { problem: CronProblem } {
  const parts = expr.trim().toLowerCase().split(/\s+/).filter(Boolean)
  if (parts.length !== 5) return { problem: { kind: 'count', n: parts.length } }
  const fields: CronField[] = []
  for (const [i, spec] of SPECS.entries()) {
    const f = parseField(spec, parts[i] ?? '')
    if ('kind' in f) return { problem: f }
    fields.push(f)
  }
  const [minute, hour, dom, month, dow] = fields as [
    CronField,
    CronField,
    CronField,
    CronField,
    CronField,
  ]
  const dayAnd = (parts[2] ?? '').startsWith('*') || (parts[4] ?? '').startsWith('*')
  return { cron: { minute, hour, dom, month, dow, dayAnd } }
}

// The longest month by month number (February 29 exists in leap years).
const MONTH_DAYS = [0, 31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31]

/**
 * True when no date matches: the days of the month asked for do not exist in any month asked for ("0 6 31 2 *").
 * A weekday field that may match on its own ("0 6 31 2 1") always finds a day.
 */
export function neverRuns(c: Cron): boolean {
  if (!c.dayAnd) return false
  return !c.month.values.some((m) => c.dom.values.some((d) => d <= (MONTH_DAYS[m] ?? 0)))
}

/** What the sentence needs from the page: translation, a locale list ("1, 15 and 28") and weekday and month names. */
export interface Namers {
  t(key: string, params: Record<string, string | number>, n?: number): string
  list(items: string[]): string
  weekday(d: number): string
  month(m: number): string
  /** A time of day in the page's clock. */
  time(h: number, m: number): string
}

const pad = (n: number) => String(n).padStart(2, '0')
const isAll = (f: CronField) => f.star && f.every === null
const P = 'automations.schedule.cronWords.'

/**
 * The schedule in words for the common shapes ("06:00 on day 1 of every month", "Every 15 minutes"), or null when
 * the expression is better shown as it is.
 */
export function describeCron(c: Cron, n: Namers): string | null {
  if (c.minute.every !== null && isAll(c.hour)) {
    if (!isAll(c.dom) || !isAll(c.month) || !isAll(c.dow)) return null
    return n.t(`${P}everyMinutes`, { count: c.minute.every }, c.minute.every)
  }
  if (c.minute.star || c.minute.values.length !== 1) return null
  const minute = c.minute.values[0] ?? 0
  let time: string
  const hourly = isAll(c.hour)
  if (hourly) time = n.t(`${P}hourly`, { minute: pad(minute) })
  else if (!c.hour.star && c.hour.values.length <= 4)
    time = n.list(c.hour.values.map((h) => n.time(h, minute)))
  else return null

  let days: string
  const anyDom = isAll(c.dom)
  const anyDow = isAll(c.dow)
  if (anyDom && anyDow) days = hourly ? '' : n.t(`${P}daily`, {})
  else if (anyDom && !c.dow.star)
    days =
      c.dow.values.join() === '1,2,3,4,5'
        ? n.t(`${P}weekdays`, {})
        : n.t(`${P}weekly`, { days: n.list(c.dow.values.map((v) => n.weekday(v))) })
  else if (anyDow && !c.dom.star && c.dom.values.length <= 4)
    days = n.t(`${P}monthly`, { days: n.list(c.dom.values.map(String)) }, c.dom.values.length)
  else return null

  const text = days ? n.t(`${P}sentence`, { time, days }) : time
  if (isAll(c.month)) return text
  if (c.month.star || c.month.values.length > 4) return null
  return n.t(`${P}inMonths`, { text, months: n.list(c.month.values.map((m) => n.month(m))) })
}
