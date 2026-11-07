import { describe, expect, it } from 'vitest'

import { describeCron, neverRuns, parseCron, type Cron, type Namers } from './cronText'

function cron(expr: string): Cron {
  const parsed = parseCron(expr)
  if (!('cron' in parsed)) throw new Error(`${expr}: ${JSON.stringify(parsed.problem)}`)
  return parsed.cron
}

// English words with the keys' last part, enough to read the sentence the page would show.
const EN: Record<string, (p: Record<string, string | number>, n?: number) => string> = {
  everyMinutes: (p, n) => (n === 1 ? 'Every minute' : `Every ${p.count} minutes`),
  hourly: (p) => `Every hour at :${p.minute}`,
  daily: () => 'every day',
  weekdays: () => 'Monday to Friday',
  weekly: (p) => `every ${p.days}`,
  monthly: (p, n) => `on ${n === 1 ? 'day' : 'days'} ${p.days} of every month`,
  sentence: (p) => `${p.time} ${p.days}`,
  inMonths: (p) => `${p.text}, in ${p.months}`,
}
const DAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']
const namers: Namers = {
  t: (key, params, n) => EN[key.split('.').pop() ?? '']?.(params, n) ?? key,
  list: (items) => items.join(' and '),
  weekday: (d) => DAYS[d] ?? '',
  month: (m) => ['', 'January', 'February', 'March', 'April', 'May', 'June', 'July'][m] ?? '',
  time: (h, m) => `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`,
}
const words = (expr: string) => describeCron(cron(expr), namers)

describe('cronText', () => {
  it('names what is wrong the way the daemon refuses it', () => {
    expect(parseCron('* * *')).toEqual({ problem: { kind: 'count', n: 3 } })
    expect(parseCron('61 * * * *')).toEqual({
      problem: { kind: 'range', field: 'minute', value: '61', min: 0, max: 59 },
    })
    expect(parseCron('0 6 * * funday')).toEqual({
      problem: { kind: 'value', field: 'dow', value: 'funday' },
    })
    expect(parseCron('*/0 * * * *')).toEqual({
      problem: { kind: 'step', field: 'minute', value: '0' },
    })
    expect(parseCron('0 1-2-3 * * *')).toEqual({
      problem: { kind: 'part', field: 'hour', value: '1-2-3' },
    })
    expect(parseCron('0 6,, * * *')).toMatchObject({ problem: { kind: 'part', field: 'hour' } })
    expect(parseCron('0 9 * JAN-mar mon-FRI')).toHaveProperty('cron')
    expect(cron('0 0 * * 7').dow.values).toEqual([0])
  })

  it('knows a schedule no date matches', () => {
    expect(neverRuns(cron('0 6 31 2 *'))).toBe(true)
    expect(neverRuns(cron('0 6 30,31 2 *'))).toBe(true)
    expect(neverRuns(cron('0 6 31 4,6,9,11 *'))).toBe(true)
    expect(neverRuns(cron('0 6 29 2 *'))).toBe(false)
    expect(neverRuns(cron('0 6 31 1-2 *'))).toBe(false)
    // A day matching the weekday field runs on its own (Vixie cron's OR).
    expect(neverRuns(cron('0 6 31 2 1'))).toBe(false)
  })

  it('puts the common shapes in words and leaves the rest as they are', () => {
    expect(words('0 6 1 * *')).toBe('06:00 on day 1 of every month')
    expect(words('0 6 1,15 * *')).toBe('06:00 on days 1 and 15 of every month')
    expect(words('30 9 * * *')).toBe('09:30 every day')
    expect(words('0 9,18 * * 1-5')).toBe('09:00 and 18:00 Monday to Friday')
    expect(words('0 8 * * sat,sun')).toBe('08:00 every Sunday and Saturday')
    expect(words('15 * * * *')).toBe('Every hour at :15')
    expect(words('*/15 * * * *')).toBe('Every 15 minutes')
    expect(words('0 6 1 1,7 *')).toBe('06:00 on day 1 of every month, in January and July')
    expect(words('0 */2 * * *')).toBeNull()
    expect(words('0 6 1 * 1')).toBeNull()
    expect(words('*/5 9-17 * * *')).toBeNull()
  })
})
