import { describe, expect, it } from 'vitest'

import { iso, makeTask, T0 } from '@/test/fixtures'

import { buildBoard, nextStep, sameBoard } from './watchModel'

const attention = (state: 'question' | 'blocked' | 'stalled', since: string) => ({
  state,
  reason: state,
  who: 'lead' as const,
  since,
})

describe('watch board', () => {
  const tasks = [
    makeTask({ id: 'run-old', status: 'running', createdAt: iso(-5000) }),
    makeTask({ id: 'run-new', status: 'running', createdAt: iso(-1000) }),
    makeTask({ id: 'queued', status: 'queued' }),
    makeTask({ id: 'quota', status: 'waiting_quota' }),
    makeTask({ id: 'review', status: 'done', needsSince: iso(-60_000) }),
    makeTask({ id: 'review-older', status: 'done', needsSince: iso(-120_000) }),
    makeTask({
      id: 'question',
      status: 'running',
      attention: attention('question', iso(-1000)),
    }),
    makeTask({ id: 'stalled', status: 'running', attention: attention('stalled', iso(-9000)) }),
    makeTask({ id: 'failed', status: 'failed', updatedAt: iso(-3600_000) }),
    makeTask({ id: 'failed-old', status: 'failed', updatedAt: iso(-30 * 3600_000) }),
    makeTask({ id: 'deps', status: 'blocked_deps' }),
    makeTask({
      id: 'accepted',
      status: 'done',
      review: { verdict: 'accepted', at: iso() },
      updatedAt: iso(-10),
    }),
    makeTask({ id: 'helper', status: 'done', kind: 'commit-msg' }),
  ]

  it('puts what needs you first, by urgency and then by how long it has waited', () => {
    const board = buildBoard(tasks, T0)
    expect(board.needs.map((n) => [n.id, n.kind])).toEqual([
      ['question', 'question'],
      ['stalled', 'stalled'],
      ['failed', 'failed'],
      ['deps', 'depsBlocked'],
      ['review-older', 'review'],
      ['review', 'review'],
    ])
  })

  it('puts a finished task with a red gate under needs action, not under awaiting review', () => {
    const board = buildBoard(
      [
        makeTask({ id: 'green', status: 'done', needsSince: iso(-120_000) }),
        makeTask({
          id: 'red',
          status: 'done',
          needsSince: iso(-60_000),
          gate: { state: 'red', autoFixes: 2 },
          attention: { state: 'blocked', reason: 'gate_red', who: 'lead', since: iso(-60_000) },
        }),
      ],
      T0,
    )
    expect(board.needs.map((n) => [n.id, n.kind])).toEqual([
      ['red', 'blocked'],
      ['green', 'review'],
    ])
  })

  it('lists running work (running, then waiting for quota, then queued) apart from what needs you', () => {
    const board = buildBoard(tasks, T0)
    expect(board.running).toEqual(['run-new', 'run-old', 'quota', 'queued'])
    expect(board.queued).toBe(1)
  })

  it('lists recent finished work, leaving out helper runs and old failures move to history', () => {
    const board = buildBoard(tasks, T0)
    expect(board.recent).toEqual(['accepted', 'failed-old'])
    expect(board.needs.some((n) => n.id === 'helper')).toBe(false)
  })

  it('compares boards by content so an unrelated update keeps the same lists', () => {
    const a = buildBoard(tasks, T0)
    const renamed = tasks.map((task) =>
      task.id === 'run-old' ? { ...task, title: 'Renamed' } : task,
    )
    expect(sameBoard(a, buildBoard(renamed, T0))).toBe(true)
    const finished = tasks.map((task) =>
      task.id === 'run-old' ? { ...task, status: 'cancelled' as const } : task,
    )
    expect(sameBoard(a, buildBoard(finished, T0))).toBe(false)
  })

  it('suggests the next step per kind', () => {
    expect([nextStep('question'), nextStep('review'), nextStep('failed')]).toEqual([
      'answer',
      'review',
      'open',
    ])
  })
})
