import { describe, expect, it } from 'vitest'

import type { Project, ProofCounts, ProofIndex, ProofRun } from '@/api/types'

import { countParts, repoOptions, runTone, statusTone, total, withRun } from './proofModel'

const counts = (extra: Partial<ProofCounts> = {}): ProofCounts => ({
  unmapped: 0,
  in_progress: 0,
  unproven: 0,
  proven: 0,
  regressed: 0,
  failing: 0,
  ...extra,
})

const run = (id: string, startedAt: string, extra: Partial<ProofRun> = {}): ProofRun => ({
  id,
  repoRoot: 'D:/Work/app',
  state: 'running',
  startedAt,
  counts: { green: 0, red: 0, error: 0 },
  total: 1,
  results: [],
  ...extra,
})

describe('proof model', () => {
  it('offers each repository once, by its root, and a project without a root by its folder', () => {
    const projects = [
      { key: 'd:/work/app/web', name: 'web', cwd: 'D:/Work/app/web', repoRoot: 'D:/Work/app' },
      { key: 'd:/work/app', name: 'app', cwd: 'D:/Work/app', repoRoot: 'd:\\work\\app' },
      { key: 'd:/notes', name: 'notes', cwd: 'D:/notes', repoRoot: '' },
    ] as unknown as Project[]
    expect(repoOptions(projects)).toEqual([
      { cwd: 'D:/Work/app', name: 'app' },
      { cwd: 'D:/notes', name: 'notes' },
    ])
  })

  it('orders the counts, drops the empty ones and adds them up', () => {
    const c = counts({ failing: 1, proven: 3, unmapped: 2 })
    expect(countParts(c)).toEqual([
      { status: 'proven', n: 3 },
      { status: 'unmapped', n: 2 },
      { status: 'failing', n: 1 },
    ])
    expect(total(c)).toBe(6)
    expect(total(counts())).toBe(0)
  })

  it('gives each status and run state a tone', () => {
    expect(statusTone('proven')).toBe('success')
    expect(statusTone('regressed')).toBe('danger')
    expect(statusTone('unproven')).toBe('attention')
    expect(statusTone('unmapped')).toBe('neutral')
    expect(runTone('red')).toBe('danger')
    expect(runTone('cancelled')).toBe('muted')
  })

  it('merges a live run newest first and ignores runs of another repository', () => {
    const index = {
      repoRoot: 'D:/Work/app',
      runs: [run('r1', '2026-11-06T10:00:00Z', { state: 'green' })],
    } as unknown as ProofIndex
    const next = withRun(index, run('r2', '2026-11-06T11:00:00Z'))
    expect(next.runs.map((r) => r.id)).toEqual(['r2', 'r1'])
    const updated = withRun(next, run('r2', '2026-11-06T11:00:00Z', { state: 'red' }))
    expect(updated.runs.map((r) => [r.id, r.state])).toEqual([
      ['r2', 'red'],
      ['r1', 'green'],
    ])
    expect(withRun(index, run('r9', '2026-11-06T12:00:00Z', { repoRoot: 'D:/Other' }))).toBe(index)
    // The same repository spelled another way is the same repository.
    expect(
      withRun(index, run('r3', '2026-11-06T12:00:00Z', { repoRoot: 'd:\\work\\app' })).runs,
    ).toHaveLength(2)
  })
})
