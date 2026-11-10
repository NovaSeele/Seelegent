import { describe, expect, it } from 'vitest'

import { iso, makeTask, T0 } from '@/test/fixtures'

import {
  canCancel,
  canFollowup,
  displayStatus,
  isVisibleTask,
  NEED_WORD_KEY,
  needKind,
  slotWaitDetail,
  statusWordKey,
} from './taskStatus'

const attention = (state: 'blocked' | 'question' | 'stalled' | 'info') => ({
  state,
  reason: 'r',
  who: 'lead' as const,
  since: iso(),
})

describe('task status', () => {
  it('maps API status, attention and verdict to one display status', () => {
    expect(displayStatus(makeTask({ status: 'running' }))).toBe('running')
    expect(displayStatus(makeTask({ status: 'running', attention: attention('question') }))).toBe(
      'blocked',
    )
    expect(displayStatus(makeTask({ status: 'running', attention: attention('info') }))).toBe(
      'running',
    )
    expect(displayStatus(makeTask({ status: 'waiting_quota' }))).toBe('waiting')
    // The same status while free memory is low: its own word, never the quota's.
    const memory = makeTask({ status: 'waiting_quota', waiting: 'memory' })
    expect(displayStatus(memory)).toBe('waitingResources')
    expect(statusWordKey(memory)).toBe('common.status.waitingResources.label')
    expect(displayStatus(makeTask({ status: 'done' }))).toBe('review')
    const review = { verdict: 'accepted' as const, at: iso() }
    expect(displayStatus(makeTask({ status: 'done', review }))).toBe('accepted')
    expect(
      displayStatus(makeTask({ status: 'done', review: { ...review, verdict: 'rejected' } })),
    ).toBe('rejected')
    expect(displayStatus(makeTask({ status: 'blocked_deps' }))).toBe('blockedDeps')
    expect(displayStatus(makeTask({ status: 'failed', attention: attention('blocked') }))).toBe(
      'failed',
    )
  })

  it('says why a task needs someone, and forgets failures after a day', () => {
    const now = T0 + 1000
    expect(needKind(makeTask({ attention: attention('question') }), now)).toBe('question')
    expect(needKind(makeTask({ attention: attention('stalled') }), now)).toBe('stalled')
    expect(needKind(makeTask({ status: 'done' }), now)).toBe('review')
    expect(needKind(makeTask({ status: 'failed' }), now)).toBe('failed')
    expect(needKind(makeTask({ status: 'failed' }), T0 + 25 * 3600_000)).toBeNull()
    expect(needKind(makeTask({ status: 'blocked_deps' }), now)).toBe('depsBlocked')
    expect(needKind(makeTask({ status: 'running' }), now)).toBeNull()
    expect(needKind(makeTask({ status: 'done', kind: 'commit-msg' }), now)).toBeNull()
  })

  it('says a finished task with a red gate needs action, as its pill does, not that it awaits review', () => {
    const gateRed = makeTask({
      status: 'done',
      review: null,
      gate: { state: 'red', autoFixes: 2 },
      attention: { state: 'blocked', reason: 'gate_red', who: 'lead', since: iso() },
    })
    expect(needKind(gateRed)).toBe('blocked')
    expect(displayStatus(gateRed)).toBe('blocked')
    expect(statusWordKey(gateRed)).toBe(NEED_WORD_KEY.blocked)
    // A green gate leaves only the review flag (info): still awaiting review.
    const green = makeTask({
      status: 'done',
      review: null,
      gate: { state: 'green', autoFixes: 0 },
      attention: { state: 'info', reason: 'review', who: 'lead', since: iso() },
    })
    expect(needKind(green)).toBe('review')
    expect(
      needKind({ ...gateRed, review: { verdict: 'accepted', at: iso() }, attention: null }),
    ).toBe(null)
  })

  it('hides helper runs and gates actions by state', () => {
    expect(isVisibleTask(makeTask({ kind: 'consult' }))).toBe(false)
    expect(isVisibleTask(makeTask({ kind: 'verify' }))).toBe(true)
    expect(canFollowup(makeTask({ status: 'running' }))).toBe(false)
    expect(canFollowup(makeTask({ status: 'done' }))).toBe(true)
    expect(canFollowup(makeTask({ status: 'done', manual: true }))).toBe(false)
    expect(canCancel(makeTask({ status: 'waiting_deps' }))).toBe(true)
    expect(canCancel(makeTask({ status: 'done' }))).toBe(false)
  })
})

describe('waiting_quota by reason', () => {
  const noAccounts = {
    state: 'blocked' as const,
    reason: 'no_accounts',
    who: 'user' as const,
    since: iso(),
  }
  it.each([
    // A full account with quota left (field report: this read "Waiting for quota").
    [{ waiting: 'busy' as const }, 'waitingWorker', 'common.status.waitingWorker.label'],
    [{ waiting: 'resting' as const }, 'waiting', 'common.status.waiting.label'],
    [{ waiting: 'memory' as const }, 'waitingResources', 'common.status.waitingResources.label'],
    [
      { waiting: 'no_account' as const, attention: noAccounts },
      'blocked',
      'common.attention.noAccounts',
    ],
    [{ waiting: 'no_account' as const }, 'waiting', 'common.attention.noAccounts'],
    // An older daemon sends no reason: quota, as before.
    [{}, 'waiting', 'common.status.waiting.label'],
  ])('%o shows %s with %s', (fields, display, key) => {
    const task = makeTask({ status: 'waiting_quota', ...fields })
    expect(displayStatus(task)).toBe(display)
    expect(statusWordKey(task)).toBe(key)
  })

  it('keeps a sign-in flag on a no_account wait', () => {
    const auth = { ...noAccounts, reason: 'auth' }
    const task = makeTask({ status: 'waiting_quota', waiting: 'no_account', attention: auth })
    expect(statusWordKey(task)).toBe(NEED_WORD_KEY.blocked)
  })

  it('gives the full accounts only for a slot wait', () => {
    const busy = { status: 'waiting_quota' as const, waiting: 'busy' as const }
    expect(slotWaitDetail({ ...busy, waitDetail: 'acc6: 2 of 2 running' })).toBe(
      'acc6: 2 of 2 running',
    )
    expect(slotWaitDetail(busy)).toBe('')
    expect(slotWaitDetail({ ...busy, waiting: 'resting', waitDetail: 'all resting' })).toBe('')
    expect(slotWaitDetail({ ...busy, status: 'running', waitDetail: 'stale' })).toBe('')
  })
})

describe('status words', () => {
  it('names a blocked task by why it waits, the same word the Watch board uses', () => {
    const question = makeTask({ status: 'running', attention: attention('question') })
    expect(statusWordKey(question)).toBe('common.attention.question')
    expect(NEED_WORD_KEY.question).toBe(statusWordKey(question))
    const stalled = makeTask({ status: 'running', attention: attention('stalled') })
    expect(statusWordKey(stalled)).toBe(NEED_WORD_KEY.stalled)
    const blocked = makeTask({ status: 'running', attention: attention('blocked') })
    expect(statusWordKey(blocked)).toBe(NEED_WORD_KEY.blocked)
    expect(statusWordKey(makeTask({ status: 'done', review: null }))).toBe(NEED_WORD_KEY.review)
    expect(statusWordKey(makeTask({ status: 'failed' }))).toBe(NEED_WORD_KEY.failed)
  })
})
