import { useI18n } from 'vue-i18n'

import type { Schedule } from '@/api/types'
import { useFormat } from '@/composables/useFormat'

import { clockTime, scheduleWords, weekdayName } from './automationModel'
import { describeCron, parseCron, type CronProblem } from './cronText'

/** The browser's own zone ("Asia/Bangkok"), or "" when Intl does not say. */
function browserZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone ?? ''
  } catch {
    return ''
  }
}

/**
 * Schedules and run times in words: a schedule as a sentence (a cron expression too, when it has a common shape),
 * what is wrong with a cron expression, and a time in an automation's zone with the viewer's own time beside it.
 */
export function useScheduleText() {
  const { t, locale } = useI18n()
  const fmt = useFormat()

  function list(items: string[]): string {
    try {
      return new Intl.ListFormat(locale.value, { type: 'conjunction' }).format(items)
    } catch {
      return items.join(', ')
    }
  }

  const monthName = (m: number) =>
    new Intl.DateTimeFormat(locale.value, { month: 'long', timeZone: 'UTC' }).format(
      new Date(Date.UTC(2024, m - 1, 1)),
    )

  /** The schedule as a sentence; a cron expression it cannot put in words stays as it is. */
  function schedule(s: Schedule): string {
    if (s.cron) {
      const parsed = parseCron(s.cron)
      const words =
        'cron' in parsed
          ? describeCron(parsed.cron, {
              t: (key, params, n) => (n === undefined ? t(key, params) : t(key, params, n)),
              list,
              weekday: (d) => weekdayName(d, locale.value),
              month: monthName,
              time: (h, m) => clockTime(h, m, locale.value),
            })
          : null
      return words ?? t('automations.schedule.cron', { expr: s.cron })
    }
    const w = scheduleWords(s, locale.value)
    return t(w.key, w.params)
  }

  /** What is wrong with a cron expression, in the page's language. */
  function cronProblem(p: CronProblem): string {
    if (p.kind === 'count') return t('automations.cronError.count', { n: p.n })
    const field = t(`automations.cronField.${p.field}`)
    if (p.kind === 'range')
      return t('automations.cronError.range', { field, value: p.value, min: p.min, max: p.max })
    return t(`automations.cronError.${p.kind}`, { field, value: p.value })
  }

  /** `zone` is "local" (the daemon's own zone, the viewer's too on this machine) or an IANA zone. */
  function zoneOf(zone: string): string | undefined {
    return zone === 'local' || !zone ? undefined : zone
  }

  /** A run time in the automation's zone, with its weekday; the viewer's own time is added in brackets. */
  function runTime(iso: string, zone: string): string {
    const tz = zoneOf(zone)
    const opts: Intl.DateTimeFormatOptions = {
      weekday: 'short',
      month: 'short',
      day: 'numeric',
      // The locale's own clock, as in the schedule sentence (clockTime).
      hour: 'numeric',
      minute: '2-digit',
    }
    let there: string
    try {
      there = fmt.value.date(iso, tz ? { ...opts, timeZone: tz, timeZoneName: 'short' } : opts)
    } catch {
      // A zone this browser does not know: the viewer's time alone, never a wrong one.
      return fmt.value.date(iso, opts)
    }
    if (!tz || tz === browserZone()) return there
    return t('automations.time.withLocal', { when: there, local: fmt.value.date(iso, opts) })
  }

  return { schedule, cronProblem, runTime }
}
