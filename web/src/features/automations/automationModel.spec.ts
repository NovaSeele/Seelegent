import { describe, expect, it } from 'vitest'

import type { Automation } from '@/api/types'

import {
  automationState,
  changesRun,
  formSchedule,
  problems,
  scheduleWords,
  toForm,
  toInput,
  toPatch,
  weekdayName,
} from './automationModel'

const automation = (overrides: Partial<Automation> = {}): Automation => ({
  id: 'a1',
  name: 'Nightly',
  prompt: 'Tidy the changelog',
  cwd: 'D:\\work\\app',
  schedule: { every: 'daily', at: '07:30' },
  timezone: 'local',
  gate: null,
  gateCommands: [],
  lane: [],
  noLane: false,
  quota: { minRemaining: 20, useBeforeReset: false },
  graceMinutes: 60,
  enabled: true,
  enableRequested: false,
  revision: 'abc',
  running: false,
  lastRun: null,
  createdAt: '2026-11-07T00:00:00.000Z',
  updatedAt: '2026-11-07T00:00:00.000Z',
  ...overrides,
})

describe('automationModel', () => {
  it('round-trips an automation through the form without changing what a run does', () => {
    const a = automation({ lane: ['src/'], gateCommands: ['go test ./...'], gate: true })
    const f = toForm(a)
    expect(changesRun(f, a)).toBe(false)
    expect(toPatch(f)).toMatchObject({ prompt: a.prompt, cwd: a.cwd, lane: ['src/'], gate: true })
  })

  it('sees a change of the prompt, the folder, the lane or the schedule', () => {
    const a = automation()
    expect(changesRun({ ...toForm(a), prompt: 'Delete everything' }, a)).toBe(true)
    expect(changesRun({ ...toForm(a), cwd: 'D:\\other' }, a)).toBe(true)
    expect(changesRun({ ...toForm(a), lane: 'docs/' }, a)).toBe(true)
    expect(changesRun({ ...toForm(a), at: '08:00' }, a)).toBe(true)
    expect(changesRun({ ...toForm(a), name: 'Renamed' }, a)).toBe(false)
  })

  it('builds the schedule of the form', () => {
    const f = toForm(null)
    expect(formSchedule({ ...f, every: 'weekly', weekday: 3 })).toEqual({
      every: 'weekly',
      at: '09:00',
      weekday: 3,
    })
    expect(formSchedule({ ...f, mode: 'cron', cron: ' 0 9 * * 1 ' })).toEqual({ cron: '0 9 * * 1' })
  })

  it('creates without a gate choice unless one is made, and never sends enabled', () => {
    const input = toInput({ ...toForm(null), prompt: 'x', cwd: '/srv/app' })
    expect(input).not.toHaveProperty('gate')
    expect(input).not.toHaveProperty('enabled')
    expect(toInput({ ...toForm(null), gate: 'off' }).gate).toBe(false)
  })

  it('refuses what the daemon would refuse', () => {
    const ok = { ...toForm(null), prompt: 'x', cwd: '/srv/app' }
    expect(problems(ok)).toEqual([])
    expect(problems({ ...ok, prompt: ' ' })).toEqual(['prompt'])
    expect(problems({ ...ok, cwd: 'relative/dir' })).toEqual(['cwd'])
    expect(problems({ ...ok, at: '25:00' })).toEqual(['at'])
    expect(problems({ ...ok, mode: 'cron', cron: '0 9 * *' })).toEqual(['cron'])
    expect(problems({ ...ok, minRemaining: 101, graceMinutes: -1 })).toEqual([
      'minRemaining',
      'graceMinutes',
    ])
  })

  it('says a schedule in words', () => {
    expect(scheduleWords({ every: 'hourly', at: '00:15' }, 'en')).toEqual({
      key: 'automations.schedule.hourly',
      params: { minute: '15' },
    })
    expect(scheduleWords({ every: 'weekly', at: '09:00', weekday: 0 }, 'en').params.day).toBe(
      'Sunday',
    )
    expect(scheduleWords({ cron: '*/5 * * * *' }, 'en').key).toBe('automations.schedule.cron')
    expect(weekdayName(1, 'en')).toBe('Monday')
  })

  it('names the state: on, waiting for the user, or off', () => {
    expect(automationState({ enabled: true, enableRequested: false })).toBe('on')
    expect(automationState({ enabled: false, enableRequested: true })).toBe('requested')
    expect(automationState({ enabled: false, enableRequested: false })).toBe('off')
  })
})
