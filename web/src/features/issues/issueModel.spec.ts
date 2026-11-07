import { describe, expect, it } from 'vitest'

import type { Issue } from '@/api/types'

import {
  failingChecks,
  inboxOrder,
  isAbsolutePath,
  logText,
  safeUrl,
  ticketDroppable,
  ticketFinished,
  ticketPushable,
  ticketTaskStatus,
} from './issueModel'

const issue = (key: string, overrides: Partial<Issue> = {}): Issue =>
  ({ key, title: key, state: 'open', updatedAt: '2026-11-07T00:00:00.000Z', ...overrides }) as Issue

describe('issueModel', () => {
  it('lists undecided open issues first, newest first in each group', () => {
    const list = [
      issue('#1', {
        updatedAt: '2026-11-07T03:00:00.000Z',
        decision: { action: 'skip', by: 'user', at: '' },
      }),
      issue('#2', { updatedAt: '2026-11-07T01:00:00.000Z' }),
      issue('#3', { updatedAt: '2026-11-07T02:00:00.000Z' }),
      issue('#4', { state: 'closed', updatedAt: '2026-11-07T04:00:00.000Z' }),
    ]
    expect(inboxOrder(list).map((i) => i.key)).toEqual(['#3', '#2', '#4', '#1'])
  })

  it('counts the conclusions the daemon counts as failed', () => {
    const checks = {
      state: 'failure' as const,
      runs: [
        { kind: 'check' as const, name: 'lint', status: 'completed', conclusion: 'failure' },
        { kind: 'check' as const, name: 'unit', status: 'completed', conclusion: 'timed_out' },
        { kind: 'check' as const, name: 'docs', status: 'completed', conclusion: 'success' },
        { kind: 'status' as const, name: 'ci', status: 'in_progress' },
      ],
    }
    expect(failingChecks(checks)).toEqual(['lint', 'unit'])
    expect(failingChecks(null)).toEqual([])
  })

  it('follows the daemon on what a ticket allows', () => {
    expect(ticketPushable('ready')).toBe(true)
    expect(ticketPushable('pr_open')).toBe(true)
    expect(ticketPushable('working')).toBe(false)
    expect(ticketFinished('failed')).toBe(true)
    expect(ticketFinished('review')).toBe(false)
    expect(ticketDroppable('failed')).toBe(true)
    expect(ticketDroppable('done')).toBe(false)
  })

  it('shows a ticket task like any task', () => {
    expect(ticketTaskStatus({ status: 'done' })).toBe('review')
    expect(ticketTaskStatus({ status: 'done', verdict: 'accepted' })).toBe('accepted')
    expect(ticketTaskStatus({ status: 'waiting_quota' })).toBe('waiting')
  })

  it('links only http(s) and accepts only absolute folders', () => {
    expect(safeUrl('https://github.com/o/r/pull/1')).toBe('https://github.com/o/r/pull/1')
    expect(safeUrl('javascript:alert(1)')).toBeUndefined()
    expect(isAbsolutePath('D:\\work\\app')).toBe(true)
    expect(isAbsolutePath('/srv/app')).toBe(true)
    expect(isAbsolutePath('app')).toBe(false)
  })

  it('writes a log step on one short line', () => {
    expect(logText({ files: 2, message: 'fix', empty: '' })).toBe('files: 2 · message: fix')
    expect(logText({ text: 'x'.repeat(300) }, 20)).toHaveLength(20)
  })
})
