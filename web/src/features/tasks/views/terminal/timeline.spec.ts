import { describe, expect, it } from 'vitest'

import type { TaskEvent } from '@/api/events'
import { makeEvent } from '@/test/fixtures'

import { buildTimeline, sameTimeline } from './timeline'

let seq = 0
const ev = (type: TaskEvent['type'], data: Record<string, unknown>) =>
  makeEvent('t1', ++seq, data, type)
const worker = (data: Record<string, unknown>) => ev('worker', data)

describe('terminal timeline', () => {
  it('merges text pieces of one step and splits at a tool or a new step', () => {
    seq = 0
    const items = buildTimeline([
      worker({ kind: 'message', step: 1, text: 'Reading ' }),
      worker({ kind: 'message', step: 1, text: 'the handler.' }),
      worker({
        kind: 'tool_call',
        step: 2,
        tool: { id: 'c1', name: 'read_file', state: 'calling' },
      }),
      worker({ kind: 'message', step: 3, text: 'Done.' }),
      worker({ kind: 'message', step: 4, text: 'Next.' }),
    ])
    expect(items.map((i) => i.kind)).toEqual(['text', 'tool', 'text', 'text'])
    expect(items[0]).toMatchObject({
      kind: 'text',
      text: 'Reading the handler.',
      key: 'e1',
      rev: 2,
    })
  })

  it('words a recovery notice from its code, never from the daemon English', () => {
    seq = 0
    const english = 'the mask list could not be read'
    const items = buildTimeline([
      ev('reattach', { accountId: 'a1' }),
      ev('resumed', { accountId: 'a1', noticeCode: 'mask_unreadable', notice: english }),
      ev('reattach', { accountId: 'a1', noticeCode: 'from_a_newer_daemon', notice: english }),
      ev('reattach', { accountId: 'a1', noticeCode: 'Not a code', notice: english }),
    ])
    expect(items).toHaveLength(4)
    expect(items[0]).toMatchObject({
      kind: 'notice',
      tone: 'dim',
      message: { key: 'terminal.ev.reattach' },
    })
    expect(items[0]).not.toHaveProperty('note')
    expect(items[1]).toMatchObject({
      kind: 'notice',
      tone: 'warn',
      message: { key: 'terminal.ev.resumed' },
      note: {
        key: 'terminal.ev.recoveryNotice.mask_unreadable',
        fallback: 'terminal.ev.recoveryNotice.unknown',
      },
    })
    expect(items[2]).toMatchObject({
      tone: 'warn',
      note: { key: 'terminal.ev.recoveryNotice.from_a_newer_daemon' },
    })
    expect(items[3]).toMatchObject({
      tone: 'warn',
      note: { key: 'terminal.ev.recoveryNotice.unknown' },
    })
    expect(JSON.stringify(items)).not.toContain(english)
  })

  it('pairs a tool call with its result and the file it touched', () => {
    seq = 0
    const items = buildTimeline([
      worker({
        kind: 'tool_call',
        step: 1,
        tool: { id: 'c1', name: 'edit_file', state: 'calling', input: { path: 'D:/app/a.go' } },
      }),
      worker({
        kind: 'file',
        step: 1,
        file: { path: 'D:/app/a.go', op: 'edit', tool: 'edit_file' },
      }),
      worker({
        kind: 'tool_result',
        step: 1,
        tool: { id: 'c1', name: 'edit_file', state: 'done', output: 'Applied', durationMs: 80 },
      }),
    ])
    expect(items).toHaveLength(1)
    expect(items[0]).toMatchObject({
      kind: 'tool',
      key: 'e1',
      rev: 3,
      file: { path: 'D:/app/a.go', op: 'edit' },
      tool: { state: 'done', output: 'Applied', durationMs: 80, input: { path: 'D:/app/a.go' } },
    })
  })

  it('keeps a file event without a tool as its own line', () => {
    seq = 0
    const items = buildTimeline([
      worker({ kind: 'file', step: 1, file: { path: 'a.ts', op: 'read' } }),
    ])
    expect(items).toEqual([expect.objectContaining({ kind: 'file', path: 'a.ts', op: 'read' })])
  })

  it('turns lead messages, routes, briefs and turn ends into their own items', () => {
    seq = 0
    const items = buildTimeline([
      ev('lead_message', { message: 'Add a limit', by: 'lead' }),
      ev('route', {
        accountId: 'a1',
        accountLabel: 'Work',
        model: 'm',
        reason: 'r',
        continuing: false,
      }),
      ev('worker_prompt', { length: 900, preview: 'You are…' }),
      worker({ kind: 'usage', usage: { inputTokens: 1, outputTokens: 1, totalTokens: 2 } }),
      ev('attempt_end', { status: 'ok' }),
      ev('turn_end', { status: 'ok', files: ['a.go'] }),
    ])
    expect(items.map((i) => i.kind)).toEqual(['lead', 'route', 'prompt', 'turnEnd'])
    expect(items[2]).toMatchObject({ attempt: 1, length: 900 })
  })

  it('maps other events to notices with an i18n key', () => {
    seq = 0
    const items = buildTimeline([
      ev('retry', { delayMs: 4500, error: 'socket hang up' }),
      ev('waiting_quota', { until: '2026-11-06T12:00:00Z' }),
      ev('attention', { state: 'cleared' }),
      ev('attempt_end', { status: 'quota', error: 'limit' }),
      worker({ kind: 'error', error: { class: 'fatal', message: 'boom' } }),
    ])
    expect(items.map((i) => (i.kind === 'notice' ? i.message.key : i.kind))).toEqual([
      'terminal.ev.retry',
      'terminal.ev.waitingQuota',
      'terminal.ev.attentionCleared',
      'terminal.ev.attempt.quota',
      'terminal.ev.worker.fatal',
    ])
    expect(items[0]).toMatchObject({
      message: { params: { seconds: 5 } },
      detail: 'socket hang up',
    })
    expect(items[1]).toMatchObject({ until: '2026-11-06T12:00:00Z' })
  })

  it('names an attention flag by its reason and never shows the daemon prose', () => {
    seq = 0
    const set = (state: string, reason: string, detail?: string) =>
      ev('attention', { state, reason, who: 'lead', since: '2026-11-06T12:00:00Z', detail })
    const items = buildTimeline([
      set('blocked', 'hygiene', '1 hygiene finding(s)'),
      set('question', 'question', 'Which port?'),
      set('background_wait', 'background_wait', 'the worker CLI waits'),
      set('info', 'review'),
      set('stalled', 'The worker went quiet'),
    ])
    const notices = items.flatMap((i) => (i.kind === 'notice' ? [i] : []))
    expect(notices.map((i) => [i.message.key, i.message.fallback, i.tone, i.detail])).toEqual([
      ['common.attention.hygiene', 'terminal.ev.attention.blocked', 'warn', undefined],
      ['common.attention.question', 'terminal.ev.attention.question', 'warn', 'Which port?'],
      [
        'common.attention.backgroundWait',
        'terminal.ev.attention.background_wait',
        'warn',
        undefined,
      ],
      ['common.attention.review', 'terminal.ev.attention.info', 'dim', undefined],
      ['terminal.ev.attention.stalled', undefined, 'warn', undefined],
    ])
  })

  it('shows work at risk once, then the restore, and counts the files still at risk', () => {
    seq = 0
    const flag = (turn: number) => ({
      ...ev('attention', {
        state: 'blocked',
        reason: 'work_at_risk',
        who: 'lead',
        since: '2026-11-06T12:00:00Z',
        detail: '2 file(s) at risk',
      }),
      turn,
    })
    const items = buildTimeline([
      ev('turn_end', { status: 'ok', files: [] }),
      ev('work_at_risk', { files: ['a.txt', 'notes.md'] }),
      flag(1),
      ev('restored', { files: ['a.txt'] }),
      flag(1),
      ev('restored', { files: ['notes.md'] }),
      ev('attention', {
        state: 'info',
        reason: 'review',
        who: 'lead',
        since: '2026-11-06T12:00:00Z',
      }),
    ])
    const notices = items.flatMap((i) => (i.kind === 'notice' ? [i] : []))
    expect(notices.map((i) => [i.message.key, i.tone])).toEqual([
      ['terminal.ev.workAtRisk', 'warn'],
      ['terminal.ev.restored', 'ok'],
      ['common.attention.workAtRisk', 'warn'],
      ['terminal.ev.restored', 'ok'],
      ['common.attention.review', 'dim'],
    ])
    expect(notices[1]).toMatchObject({ message: { params: { count: 1, n: 1 } }, detail: 'a.txt' })
    // The flag after the first restore is not a repeat: it says how many files are still at risk.
    expect(notices[2]).toMatchObject({ note: { key: 'terminal.ev.atRisk', params: { count: 1 } } })
    expect(notices[2]).not.toHaveProperty('detail')
  })

  it('names gate steps by kind and says a failed gate once', () => {
    seq = 0
    const at = (e: TaskEvent, turn: number) => ({ ...e, turn })
    const items = buildTimeline([
      at(
        ev('gate', {
          phase: 'start',
          steps: ['lane', 'syntax', 'T001', 'T001'],
          plan: [
            { kind: 'lane', label: 'lane' },
            { kind: 'auto', label: 'syntax' },
            { kind: 'task', label: 'T001', cmd: 'findstr /b a a.txt', taskIds: ['T001'] },
            { kind: 'task', label: 'T001', cmd: 'make', dir: 'web', taskIds: ['T001'] },
          ],
        }),
        3,
      ),
      at(
        ev('gate', {
          phase: 'result',
          result: {
            state: 'red',
            steps: [
              { kind: 'lane', label: 'lane', state: 'green' },
              {
                kind: 'auto',
                label: 'syntax',
                state: 'red',
                files: ['a.json', 'b.go'],
                failures: [{ file: 'a.json', message: 'unexpected end' }],
                tail: 'a.json: unexpected end',
              },
            ],
            notRun: [],
            warnings: [],
            startedAt: '2026-11-06T12:00:00Z',
          },
        }),
        3,
      ),
      at(
        ev('attention', {
          state: 'blocked',
          reason: 'gate_red',
          who: 'lead',
          since: '2026-11-06T12:00:00Z',
          detail: 'syntax check failed: a.json',
        }),
        3,
      ),
    ])
    const notices = items.flatMap((i) => (i.kind === 'notice' ? [i] : []))
    // The gate_red flag right after the red gate line of its turn adds nothing: it is left out.
    expect(notices.map((i) => i.message.key)).toEqual([
      'terminal.ev.gate.start',
      'terminal.ev.gate.red',
    ])
    expect(notices[0]?.lines).toEqual([
      { label: { key: 'task.gate.kind.lane' }, text: undefined },
      { label: { key: 'task.gate.kind.auto' }, text: undefined },
      { label: { key: 'task.gate.kind.task' }, text: 'findstr /b a a.txt · T001' },
      { label: { key: 'task.gate.kind.task' }, text: 'cd web && make · T001' },
    ])
    expect(notices[1]?.lines).toEqual([
      { label: { key: 'task.gate.kind.auto' }, text: undefined },
      { text: 'a.json: unexpected end' },
    ])
  })

  it('keeps a gate_red flag of another turn', () => {
    seq = 0
    const items = buildTimeline([
      {
        ...ev('gate', {
          phase: 'result',
          result: { state: 'red', steps: [], notRun: [], warnings: [], startedAt: '' },
        }),
        turn: 1,
      },
      {
        ...ev('attention', { state: 'blocked', reason: 'gate_red', who: 'lead', since: '' }),
        turn: 2,
      },
    ])
    expect(items.flatMap((i) => (i.kind === 'notice' ? [i.message.key] : []))).toEqual([
      'terminal.ev.gate.red',
      'common.attention.gateRed',
    ])
  })

  it('reuses unchanged items so only the item an event touched re-renders', () => {
    seq = 0
    const events = [
      ev('lead_message', { message: 'Go', by: 'lead' }),
      worker({ kind: 'message', step: 1, text: 'a' }),
    ]
    const first = buildTimeline(events)
    const again = buildTimeline(events, first)
    expect(again).toEqual(first)
    expect(sameTimeline(first, again)).toBe(true)

    const grown = buildTimeline([...events, worker({ kind: 'message', step: 1, text: 'b' })], first)
    expect(grown[0]).toBe(first[0])
    expect(grown[1]).not.toBe(first[1])
    expect(grown[1]).toMatchObject({ text: 'ab' })
    expect(sameTimeline(first, grown)).toBe(false)
  })
})
