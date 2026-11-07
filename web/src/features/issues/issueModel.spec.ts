import { describe, expect, it } from 'vitest'

import type { Issue } from '@/api/types'

import {
  branchOnRemote,
  failingChecks,
  inboxOrder,
  isAbsolutePath,
  isPushRefusal,
  needsYou,
  phaseLabel,
  prNumber,
  runState,
  ticketPhase,
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
    // A pull request can be linked only to a branch that is on GitHub.
    const none = { pr: null, refusal: undefined, outbox: [] }
    expect(branchOnRemote(none)).toBe(false)
    expect(
      branchOnRemote({
        ...none,
        refusal: { reason: 'unsafe_files', findings: [], message: '', at: '' },
      }),
    ).toBe(false)
    expect(
      branchOnRemote({
        ...none,
        refusal: { reason: 'branch_diverged', findings: [], message: '', at: '' },
      }),
    ).toBe(true)
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

  it('shows what the lead has to do rather than the bare status', () => {
    expect(ticketPhase({ status: 'pr_open', needsAttention: 'open_pr' })).toBe('open_pr')
    expect(ticketPhase({ status: 'pr_open' })).toBe('pr_open')
    expect(phaseLabel('open_pr')).toBe('issues.ticket.phase.open_pr')
    expect(phaseLabel('ready')).toBe('issues.ticket.status.ready')
    // Ready waits for someone (no tier opens pull requests on its own); working and done do not.
    expect(
      ['review', 'ready', 'push_refused', 'open_pr', 'checks_failed', 'failed'].every((p) =>
        needsYou(p as Parameters<typeof needsYou>[0]),
      ),
    ).toBe(true)
    expect(needsYou('working') || needsYou('pr_open') || needsYou('done')).toBe(false)
  })

  it('reads a pull request number from a number or its address', () => {
    expect(prNumber('43')).toBe(43)
    expect(prNumber(' #43 ')).toBe(43)
    expect(prNumber('https://github.com/o/r/pull/43')).toBe(43)
    expect(prNumber('https://github.com/o/r/pull/43/files?x=1')).toBe(43)
    expect(prNumber('0')).toBeNull()
    expect(prNumber('https://github.com/o/r/issues/43')).toBeNull()
    expect(prNumber('forty')).toBeNull()
  })

  it('names each check run by its result', () => {
    expect(runState({ status: 'in_progress' })).toBe('pending')
    expect(runState({ status: 'completed', conclusion: 'success' })).toBe('success')
    expect(runState({ status: 'completed', conclusion: 'timed_out' })).toBe('failure')
    expect(runState({ status: 'completed', conclusion: 'skipped' })).toBe('unknown')
  })

  it('tells a refused push from other errors', () => {
    expect(isPushRefusal({ reason: 'unsafe_files' })).toBe(true)
    expect(isPushRefusal({ reason: 'branch_diverged' })).toBe(true)
    expect(isPushRefusal({ reason: 'lane_conflict' })).toBe(false)
  })
})
