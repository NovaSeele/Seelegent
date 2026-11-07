import { describe, expect, it } from 'vitest'

import type { AutomationRun } from '@/api/types'
import { createFormatters } from '@/composables/useFormat'
import en from '@/i18n/locales/en/automations.json'
import common from '@/i18n/locales/en/common.json'

import { runView, type Translate } from './runText'

/** The English catalog, enough of vue-i18n's {name} interpolation for these sentences. */
const t: Translate = (key, params = {}) => {
  const [ns, ...path] = key.split('.')
  let v: unknown = ns === 'common' ? common : ns === 'automations' ? en : undefined
  for (const p of path) v = (v as Record<string, unknown> | undefined)?.[p]
  if (typeof v !== 'string') throw new Error(`no message ${key}`)
  return v.replace(/\{(\w+)\}/g, (_, name: string) => String(params[name]))
}
const fmt = createFormatters('en')
const run = (r: Partial<AutomationRun>): AutomationRun => ({
  id: 1,
  at: '2026-11-07T09:00:00.000Z',
  trigger: 'schedule',
  outcome: 'started',
  ...r,
})

describe('runView', () => {
  it('says why a run was skipped in words, with locale numbers and durations', () => {
    expect(
      runView(
        run({ outcome: 'skipped_late', info: { lateMinutes: 920, graceMinutes: 60 } }),
        t,
        fmt,
      ).why,
    ).toBe('Seelegent was off when it was due: 15h 20m late, more than its 1h grace period.')
    expect(
      runView(
        run({
          outcome: 'skipped_quota',
          info: { backend: 'agy', remaining: 12, minRemaining: 25 },
        }),
        t,
        fmt,
      ).why,
    ).toBe('The best agy account has 12% left; this automation needs at least 25%.')
    expect(
      runView(
        run({ outcome: 'skipped_quota', info: { account: 'a@x', remaining: 0, minRemaining: 40 } }),
        t,
        fmt,
      ).why,
    ).toContain('a@x that resets soon has 0% left')
    expect(
      runView(
        run({
          outcome: 'skipped_overlap',
          info: { reason: 'task_running', taskStatus: 'waiting_quota' },
        }),
        t,
        fmt,
      ).why,
    ).toBe('The task of its last run has not finished yet (Waiting for quota).')
    expect(
      runView(run({ outcome: 'skipped_overlap', info: { reason: 'starting' } }), t, fmt).why,
    ).toBe('Another run of this automation was already starting.')
  })

  it('labels a command output and keeps it verbatim', () => {
    const v = runView(
      run({ outcome: 'precheck_failed', info: { exitCode: 128, output: 'fatal: bad revision' } }),
      t,
      fmt,
    )
    expect(v).toEqual({
      why: 'The command exited with code 128.',
      output: 'fatal: bad revision',
      outputLabel: 'Command output:',
    })
    expect(runView(run({ outcome: 'skipped_precheck', info: { exitCode: 1 } }), t, fmt)).toEqual({
      why: 'The precheck exited with code 1: nothing to do this time.',
      output: '',
      outputLabel: '',
    })
    expect(
      runView(
        run({ outcome: 'precheck_failed', info: { timedOut: true, timeoutSeconds: 60 } }),
        t,
        fmt,
      ).why,
    ).toBe('The command did not finish within 1m.')
  })

  it('explains a change during the precheck, a missing member and an unknown error', () => {
    expect(
      runView(run({ outcome: 'skipped_changed', info: { reason: 'changed' } }), t, fmt).why,
    ).toMatch(/edited while its precheck ran, so nothing was started/)
    expect(
      runView(run({ outcome: 'skipped_changed', info: { reason: 'master_off' } }), t, fmt).why,
    ).toMatch(/turned off in Settings/)
    expect(
      runView(
        run({ outcome: 'error', info: { reason: 'member_missing', member: 'search-squad' } }),
        t,
        fmt,
      ).why,
    ).toBe(
      'The member "search-squad" no longer exists. Edit the automation to pick another member or a backend.',
    )
    expect(runView(run({ outcome: 'error', info: { message: 'boom' } }), t, fmt)).toEqual({
      why: 'The task could not start.',
      output: 'boom',
      outputLabel: 'Seelegent said:',
    })
  })

  it('keeps the daemon detail of an older row', () => {
    expect(runView(run({ outcome: 'error', detail: 'old text' }), t, fmt).why).toBe('old text')
  })
})
