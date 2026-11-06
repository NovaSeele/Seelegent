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
