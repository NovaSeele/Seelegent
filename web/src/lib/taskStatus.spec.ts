import { describe, expect, it } from 'vitest'

import { iso, makeTask, T0 } from '@/test/fixtures'

import {
  canCancel,
  canFollowup,
  displayStatus,
  isVisibleTask,
  NEED_WORD_KEY,
  needKind,
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
