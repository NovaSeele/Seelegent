import { describe, expect, it } from 'vitest'

import type { Turn } from '@/api/types'
import { iso } from '@/test/fixtures'

import type { TimelineItem } from '../terminal/timeline'
import { buildFiles } from './files'
import { layoutGraph, type GraphOptions } from './graph'
import { allSteps, buildProcess, stageCounts, stageOf, type Step, type TurnNode } from './process'

const tool = (
  key: string,
  turn: number,
  attempt: number,
  at: number,
  name: string,
  extra: Partial<Extract<TimelineItem, { kind: 'tool' }>['tool']> = {},
  file?: { path: string; op: 'read' | 'edit' },
): TimelineItem => ({
  key,
  rev: 1,
  turn,
  attempt,
  ts: iso(at),
  kind: 'tool',
  tool: { name, state: 'done', ...extra },
  file,
})

describe('process model', () => {
  it('classifies tools into stages', () => {
    expect(stageOf({ name: 'read_file' })).toBe('explore')
    expect(stageOf({ name: 'grep_search' })).toBe('explore')
    expect(stageOf({ name: 'edit_file' })).toBe('edit')
    expect(stageOf({ name: 'write_to_file' })).toBe('edit')
    expect(stageOf({ name: 'run_command', command: 'go test ./...' })).toBe('verify')
    expect(stageOf({ name: 'run_command', command: 'pnpm lint' })).toBe('verify')
    expect(stageOf({ name: 'run_command', command: 'git status' })).toBe('run')
    expect(stageOf({ name: 'mystery' })).toBe('run')
  })

  it('groups steps by turn and attempt, with turn records filling the gaps', () => {
    const turns = [
      {
        n: 1,
        by: 'lead',
        message: 'Do it',
        status: 'ok',
        startedAt: iso(),
        attempts: [{ accountLabel: 'A' }, { accountLabel: 'B', model: 'm' }],
      },
      { n: 2, by: 'lead', message: 'More', status: 'running', startedAt: iso(), attempts: [] },
    ] as unknown as Turn[]
    const items: TimelineItem[] = [
      tool('e1', 1, 1, 0, 'read_file', {}, { path: 'D:/app/a.go', op: 'read' }),
      { key: 'e2', rev: 2, turn: 1, attempt: 2, ts: iso(1000), kind: 'text', text: 'hi' },
      tool('e3', 1, 2, 2000, 'run_command', { command: 'go test', state: 'failed' }),
      {
        key: 'e4',
        rev: 4,
        turn: 1,
        attempt: 2,
        ts: iso(3000),
        kind: 'report',
        report: { status: 'success', response: 'Done.\nDetails' },
      },
    ]
    const tree = buildProcess(items, turns)
    expect(tree.map((t) => [t.n, t.attempts.length])).toEqual([
      [1, 2],
      [2, 0],
    ])
    expect(tree[0]?.attempts[1]).toMatchObject({ accountLabel: 'B', model: 'm' })
    const steps = allSteps(tree)
    expect(steps.map((s) => [s.key, s.stage, s.state, s.label])).toEqual([
      ['e1', 'explore', 'ok', 'D:/app/a.go'],
      ['e3', 'verify', 'failed', 'go test'],
      ['e4', 'report', 'ok', 'Done.'],
    ])
    expect(stageCounts(steps)).toEqual({ explore: 1, edit: 0, verify: 1, run: 0, report: 1 })
  })

  it('reuses the step of an unchanged item', () => {
    const items = [tool('e1', 1, 1, 0, 'read_file')]
    const cache = new WeakMap()
    const a = allSteps(buildProcess(items, undefined, cache))[0]
    const b = allSteps(buildProcess(items, undefined, cache))[0]
    expect(a).toBeDefined()
    expect(a).toBe(b)
  })
})

describe('graph layout', () => {
  const options: GraphOptions = { pxPerSec: 10, gapMs: 20_000, gapPx: 30, minWidth: 4, pad: 10 }
  const step = (key: string, at: number, ms: number, stage: Step['stage'] = 'run'): Step => ({
    key,
    turn: 1,
    attempt: 1,
    stage,
    name: 'x',
    label: '',
    state: 'ok',
    start: Date.parse(iso(at)),
    durationMs: ms,
  })
  const tree = (steps: Step[], attempt = 1, account?: string): TurnNode[] => [
    { n: 1, attempts: [{ n: attempt, accountLabel: account, steps }] },
  ]

  it('places bars by time and widens them by duration', () => {
    const layout = layoutGraph(
      tree([step('a', 0, 2000, 'explore'), step('b', 3000, 0, 'edit')]),
      options,
    )
    expect(layout.bars.map((b) => [b.step.key, b.lane, b.x, b.w])).toEqual([
      ['a', 0, 10, 20],
      ['b', 1, 40, 4],
    ])
    expect(layout.marks).toEqual([{ x: 10, turn: 1, attempt: 1, account: undefined }])
    expect(layout.spanMs).toBe(3000)
  })

  it('cuts long idle gaps to a fixed break', () => {
    const layout = layoutGraph(tree([step('a', 0, 1000), step('b', 3_600_000, 1000)]), options)
    expect(layout.breaks).toEqual([35])
    expect(layout.bars[1]?.x).toBe(50)
    expect(layout.width).toBe(70)
  })

  it('puts overlapping steps back in time and marks later attempts with their account', () => {
    const layout = layoutGraph(
      tree([step('a', 0, 5000), step('b', 1000, 1000)], 2, 'Backup'),
      options,
    )
    expect(layout.bars[1]?.x).toBe(20)
    expect(layout.marks[0]).toMatchObject({ attempt: 2, account: 'Backup' })
  })

  it('measures running steps through the callback', () => {
    const layout = layoutGraph(tree([step('a', 0, 0)]), options, () => 4000)
    expect(layout.bars[0]?.w).toBe(40)
    expect(layoutGraph([], options)).toMatchObject({ bars: [], width: 20 })
  })
})

describe('files model', () => {
  const s = (path: string, op: 'read' | 'edit', turn = 1): Step => ({
    key: path + op + turn,
    turn,
    attempt: 1,
    stage: op === 'edit' ? 'edit' : 'explore',
    name: op,
    label: path,
    path,
    op,
    state: 'ok',
    start: turn,
    durationMs: 0,
  })

  it('counts reads and edits per file, grouped by folder, with repo-relative git paths', () => {
    const model = buildFiles(
      [
        s('D:/mono/web/pages/[id].vue', 'read'),
        s('D:\\mono\\web\\pages\\[id].vue', 'edit', 2),
        s('D:/mono/web/README.md', 'read'),
        s('D:/other/notes.txt', 'read'),
      ],
      'D:/mono/web',
      'D:/mono',
    )
    expect(model).toMatchObject({ read: 3, edited: 1, outside: 1 })
    expect(model.folders.map((f) => [f.folder, f.files.map((x) => x.rel)])).toEqual([
      ['', ['README.md']],
      ['pages', ['pages/[id].vue']],
      ['D:/other', ['D:/other/notes.txt']],
    ])
    const page = model.folders[1]?.files[0]
    expect(page).toMatchObject({ reads: 1, edits: 1, turns: [1, 2], gitPath: 'web/pages/[id].vue' })
    expect(model.folders[2]?.files[0]).toMatchObject({ outside: true, gitPath: undefined })
  })

  it('accepts cwd-relative paths', () => {
    const model = buildFiles([s('src/a.ts', 'edit'), s('../x.ts', 'read')], 'D:/app', 'D:/app')
    // Files outside the project come last.
    expect(model.folders.flatMap((f) => f.files.map((x) => [x.rel, x.gitPath, x.outside]))).toEqual(
      [
        ['src/a.ts', 'src/a.ts', false],
        ['../x.ts', undefined, true],
      ],
    )
  })

  it('folds case only for Windows paths', () => {
    const windows = buildFiles(
      [s('D:/app/Src/A.ts', 'read'), s('d:\\app\\src\\a.ts', 'edit'), s('src/a.TS', 'read')],
      'D:/app',
      undefined,
    )
    expect(windows.folders.flatMap((f) => f.files)).toHaveLength(1)
    const posix = buildFiles(
      [s('/srv/app/Readme.md', 'read'), s('/srv/app/README.md', 'edit'), s('readme.md', 'read')],
      '/srv/app',
      undefined,
    )
    expect(posix.folders.flatMap((f) => f.files.map((x) => x.rel)).sort()).toEqual([
      'README.md',
      'Readme.md',
      'readme.md',
    ])
  })
})
