/**
 * What the Automations page shows and edits: the editor's form (one flat object the fields bind to), turning it
 * into a create or patch body, the checks the form makes before the daemon does, the schedule in words, and the
 * tone and icon of an automation's state and of a run's outcome (never colour alone).
 */
import type { IconName } from '@/components/ui/icons'
import type { Tone } from '@/components/ui/tone'
import type {
  Automation,
  AutomationInput,
  AutomationPatch,
  AutomationRun,
  Schedule,
} from '@/api/types'

import { neverRuns, parseCron } from './cronText'

export type Every = NonNullable<Schedule['every']>
export type ScheduleMode = 'preset' | 'cron'
export type GateChoice = 'default' | 'on' | 'off'

export const EVERY: readonly Every[] = ['hourly', 'daily', 'weekdays', 'weekly']
export const BACKENDS = ['agy', 'claude', 'codex'] as const

/** The daemon's defaults (internal/automation): 20 % quota left, a 60 minute grace for a missed run. */
export const DEFAULT_MIN_REMAINING = 20
export const DEFAULT_GRACE = 60

export interface AutomationForm {
  name: string
  prompt: string
  cwd: string
  mode: ScheduleMode
  every: Every
  at: string
  weekday: number
  cron: string
  timezone: string
  member: string
  backend: string
  model: string
  gate: GateChoice
  gateCommands: string
  lane: string
  noLane: boolean
  precheck: string
  minRemaining: number
  useBeforeReset: boolean
  graceMinutes: number
}

/** The form for a new automation, or for editing `a`. */
export function toForm(a?: Automation | null): AutomationForm {
  const s = a?.schedule
  return {
    name: a?.name ?? '',
    prompt: a?.prompt ?? '',
    cwd: a?.cwd ?? '',
    mode: s?.cron ? 'cron' : 'preset',
    every: s?.every ?? 'daily',
    at: s?.at ?? '09:00',
    weekday: s?.weekday ?? 1,
    cron: s?.cron ?? '',
    timezone: a?.timezone ?? 'local',
    member: a?.member ?? '',
    backend: a?.backend ?? '',
    model: a?.model ?? '',
    gate: a?.gate === true ? 'on' : a?.gate === false ? 'off' : 'default',
    gateCommands: (a?.gateCommands ?? []).join('\n'),
    lane: (a?.lane ?? []).join('\n'),
    noLane: a?.noLane ?? false,
    precheck: a?.precheck ?? '',
    minRemaining: a?.quota.minRemaining ?? DEFAULT_MIN_REMAINING,
    useBeforeReset: a?.quota.useBeforeReset ?? false,
    graceMinutes: a?.graceMinutes ?? DEFAULT_GRACE,
  }
}

/** The trimmed, non-empty lines of a text field. */
export function lines(text: string): string[] {
  return text
    .split(/\r?\n/)
    .map((s) => s.trim())
    .filter(Boolean)
}

/** The schedule the form describes. */
export function formSchedule(
  f: Pick<AutomationForm, 'mode' | 'every' | 'at' | 'weekday' | 'cron'>,
): Schedule {
  if (f.mode === 'cron') return { cron: f.cron.trim() }
  const s: Schedule = { every: f.every, at: f.at.trim() }
  if (f.every === 'weekly') s.weekday = f.weekday
  return s
}

/** The body of POST /automations (always created off: enabling is a separate, confirmed step). */
export function toInput(f: AutomationForm): AutomationInput {
  const input: AutomationInput = {
    name: f.name.trim(),
    prompt: f.prompt,
    cwd: f.cwd.trim(),
    schedule: formSchedule(f),
    timezone: f.timezone.trim() || 'local',
    gateCommands: lines(f.gateCommands),
    lane: lines(f.lane),
    noLane: f.noLane,
    precheck: f.precheck.trim(),
    quota: { minRemaining: f.minRemaining, useBeforeReset: f.useBeforeReset },
    graceMinutes: f.graceMinutes,
  }
  if (f.member.trim()) input.member = f.member.trim()
  if (f.backend) input.backend = f.backend
  if (f.model.trim()) input.model = f.model.trim()
  if (f.gate !== 'default') input.gate = f.gate === 'on'
  return input
}

/** The body of PATCH /automations/{id}: every field of the form (unchanged ones change nothing). */
export function toPatch(f: AutomationForm): AutomationPatch {
  const {
    name,
    prompt,
    cwd,
    schedule,
    timezone,
    gateCommands,
    lane,
    noLane,
    precheck,
    quota,
    graceMinutes,
  } = toInput(f)
  const patch: AutomationPatch = {
    name,
    prompt,
    cwd,
    schedule,
    timezone,
    member: f.member.trim(),
    backend: f.backend,
    model: f.model.trim(),
    gateCommands,
    lane,
    noLane,
    precheck,
    quota,
    graceMinutes,
  }
  if (f.gate !== 'default') patch.gate = f.gate === 'on'
  return patch
}

const sameList = (a: readonly string[], b: readonly string[]) =>
  a.length === b.length && a.every((v, i) => v === b[i])

/**
 * True when two schedules are the same as the daemon stores them (normSchedule, whose result is part of what the
 * user confirms): a cron expression trimmed, a preset by its period, its time (with the daemon's default) and, for
 * a weekly one only, its day. Comparing the raw objects would also count a missing weekday or the key order.
 */
export function sameSchedule(a: Schedule, b: Schedule): boolean {
  const cronA = a.cron?.trim() ?? ''
  const cronB = b.cron?.trim() ?? ''
  if (cronA || cronB) return cronA === cronB
  if (a.every !== b.every) return false
  const at = (s: Schedule) => s.at || (s.every === 'hourly' ? '00:00' : '09:00')
  if (at(a) !== at(b)) return false
  return a.every !== 'weekly' || (a.weekday ?? 1) === (b.weekday ?? 1)
}

/**
 * True when saving the form changes what a run does or when it runs (prompt, folder, worker, gate, lane, precheck,
 * schedule, use-before-reset or the missed-run grace): the daemon then turns an enabled automation off until it is
 * confirmed again. The quota floor alone does not: it can only skip a run.
 */
export function changesRun(f: AutomationForm, a: Automation): boolean {
  const next = toPatch(f)
  const gate = f.gate === 'default' ? a.gate : f.gate === 'on'
  return (
    next.prompt !== a.prompt ||
    next.cwd !== a.cwd ||
    (next.member ?? '') !== (a.member ?? '') ||
    (next.backend ?? '') !== (a.backend ?? '') ||
    (next.model ?? '') !== (a.model ?? '') ||
    gate !== a.gate ||
    !sameList(next.gateCommands ?? [], a.gateCommands) ||
    !sameList(next.lane ?? [], a.lane) ||
    next.noLane !== a.noLane ||
    (next.precheck ?? '') !== (a.precheck ?? '') ||
    (next.timezone ?? 'local') !== a.timezone ||
    !sameSchedule(next.schedule ?? a.schedule, a.schedule) ||
    (next.quota?.useBeforeReset ?? false) !== a.quota.useBeforeReset ||
    (next.graceMinutes ?? a.graceMinutes) !== a.graceMinutes
  )
}

export type FormProblem = 'prompt' | 'cwd' | 'at' | 'cron' | 'minRemaining' | 'graceMinutes'

const TIME = /^([01]\d|2[0-3]):[0-5]\d$/

/** The fields the form refuses before sending (the daemon checks everything again). */
export function problems(f: AutomationForm): FormProblem[] {
  const out: FormProblem[] = []
  if (!f.prompt.trim()) out.push('prompt')
  if (!/^([a-zA-Z]:[\\/]|[\\/])/.test(f.cwd.trim())) out.push('cwd')
  if (f.mode === 'preset' && !TIME.test(f.at.trim())) out.push('at')
  if (f.mode === 'cron' && cronProblem(f.cron)) out.push('cron')
  if (!Number.isFinite(f.minRemaining) || f.minRemaining < 0 || f.minRemaining > 100)
    out.push('minRemaining')
  if (!Number.isInteger(f.graceMinutes) || f.graceMinutes < 0 || f.graceMinutes > 1440)
    out.push('graceMinutes')
  return out
}

/** True when a cron expression cannot be saved: it does not parse, or no date matches it. */
export function cronProblem(expr: string): boolean {
  const parsed = parseCron(expr)
  return !('cron' in parsed) || neverRuns(parsed.cron)
}

/** The name of weekday `d` (0 = Sunday) in `locale`. */
export function weekdayName(d: number, locale: string): string {
  // 2024-01-07 was a Sunday.
  return new Intl.DateTimeFormat(locale, { weekday: 'long', timeZone: 'UTC' }).format(
    new Date(Date.UTC(2024, 0, 7 + d)),
  )
}

/**
 * A time of day in `locale`'s own clock ("6:00 AM", "06:00"): the one format of every schedule sentence and run
 * time on the page.
 */
export function clockTime(h: number, m: number, locale: string): string {
  return new Intl.DateTimeFormat(locale, {
    hour: 'numeric',
    minute: '2-digit',
    timeZone: 'UTC',
  }).format(new Date(Date.UTC(2024, 0, 1, h, m)))
}

/** "HH:MM" in the locale's clock; text that is not a time stays as it is. */
function atWords(at: string, locale: string): string {
  const m = /^(\d{1,2}):(\d{2})$/.exec(at)
  return m ? clockTime(Number(m[1]), Number(m[2]), locale) : at
}

/** The i18n key and parameters that describe a schedule in words. */
export function scheduleWords(
  s: Schedule,
  locale: string,
): { key: string; params: Record<string, string> } {
  if (s.cron) return { key: 'automations.schedule.cron', params: { expr: s.cron } }
  const raw = s.at ?? '09:00'
  const at = atWords(raw, locale)
  switch (s.every) {
    case 'hourly':
      return { key: 'automations.schedule.hourly', params: { minute: raw.slice(3) || '00' } }
    case 'weekdays':
      return { key: 'automations.schedule.weekdays', params: { at } }
    case 'weekly':
      return {
        key: 'automations.schedule.weekly',
        params: { day: weekdayName(s.weekday ?? 1, locale), at },
      }
    default:
      return { key: 'automations.schedule.daily', params: { at } }
  }
}

export type AutomationState = 'on' | 'requested' | 'off'

export function automationState(
  a: Pick<Automation, 'enabled' | 'enableRequested'>,
): AutomationState {
  if (a.enabled) return 'on'
  return a.enableRequested ? 'requested' : 'off'
}

export const STATE_LOOK: Readonly<Record<AutomationState, { tone: Tone; icon: IconName }>> = {
  on: { tone: 'success', icon: 'statusAccepted' },
  requested: { tone: 'attention', icon: 'warning' },
  off: { tone: 'muted', icon: 'statusCancelled' },
}

export const OUTCOME_LOOK: Readonly<
  Record<AutomationRun['outcome'], { tone: Tone; icon: IconName }>
> = {
  // Muted: the automation's own rules skipped the run (normal); danger: something broke and the user should look.
  started: { tone: 'success', icon: 'statusRunning' },
  skipped_precheck: { tone: 'muted', icon: 'statusCancelled' },
  precheck_failed: { tone: 'danger', icon: 'statusFailed' },
  skipped_quota: { tone: 'muted', icon: 'statusWaiting' },
  skipped_overlap: { tone: 'muted', icon: 'statusWaiting' },
  skipped_late: { tone: 'muted', icon: 'statusCancelled' },
  // Attention: an edit made while the precheck ran waits for the user's confirmation.
  skipped_changed: { tone: 'attention', icon: 'warning' },
  error: { tone: 'danger', icon: 'statusFailed' },
  pending: { tone: 'running', icon: 'statusRunning' },
}
