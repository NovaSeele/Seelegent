import { describe, expect, it } from 'vitest'

import type { Contest, ScoreRow } from '@/api/types'

import {
  defaultWinner,
  gateCell,
  letterLegend,
  pickableOut,
  qualifiedLabels,
  variantStatus,
  verifierCell,
} from './contestModel'

const row = (label: string, disqualified?: string): ScoreRow =>
  ({ label, disqualified, status: 'done', gate: 'green' }) as ScoreRow

const contest = (rows: ScoreRow[], proposal?: string): Contest =>
  ({
    scoreboard: rows,
    proposal: proposal ? { winner: proposal, source: 'machine' } : null,
  }) as Contest

describe('contestModel', () => {
  it('defaults the pick to a qualified proposal, never to a disqualified one', () => {
    const rows = [row('V1', 'gate_red'), row('V2'), row('V3')]
    expect(qualifiedLabels(contest(rows))).toEqual(['V2', 'V3'])
    expect(defaultWinner(contest(rows, 'V3'))).toBe('V3')
    // The proposal stopped qualifying: the first qualified variant, not the stale proposal.
    expect(defaultWinner(contest(rows, 'V1'))).toBe('V2')
    expect(defaultWinner(contest([row('V1', 'gate_red')], 'V1'))).toBe('')
  })

  it('offers only disqualified variants that still have work to apply', () => {
    const rows = [
      row('V1', 'gate_red'),
      row('V2', 'failed'),
      row('V3', 'no_changes'),
      row('V4', 'rejected'),
    ]
    expect(pickableOut(contest(rows)).map((r) => r.label)).toEqual(['V1', 'V4'])
  })

  it('reads statuses, gates and verdicts in the words of the task views', () => {
    expect(variantStatus('running')).toEqual({ status: 'running' })
    expect(variantStatus('done')).toEqual({ tone: 'neutral', key: 'team.contests.rowStatus.done' })
    expect(variantStatus(undefined)).toEqual({
      tone: 'muted',
      key: 'team.contests.rowStatus.missing',
    })
    expect(gateCell('green')).toEqual({ key: 'task.gate.stepState.green', tone: 'success' })
    expect(gateCell('red')).toEqual({ key: 'task.gate.stepState.red', tone: 'danger' })
    expect(gateCell('none')).toEqual({ key: 'task.gate.stepState.not_run', tone: 'muted' })
    expect(verifierCell('accept')?.tone).toBe('success')
    expect(verifierCell(undefined)).toBeNull()
  })

  it('lists the judge letters in order', () => {
    expect(letterLegend({ B: 'V2', A: 'V1' })).toEqual(['A = V1', 'B = V2'])
  })
})
