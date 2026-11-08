/**
 * The TaskEvent guard. The fixtures below stand in for internal/api/testdata/events/<type>.json until the Go side
 * writes them; a later test will load those files through the same `checkFixture`.
 */
import { describe, expect, it, vi } from 'vitest'

import {
  createEventParser,
  EVENT_REQUIRED,
  isTaskEvent,
  TASK_EVENT_TYPES,
  taskEventProblem,
  type TaskEventType,
} from './events'

const base = { seq: 3, ts: '2026-11-06T16:02:11.123Z', taskId: 't1', turn: 1, attempt: 1 }

const FIXTURES: Record<TaskEventType, Record<string, unknown>> = {
  worker: {
    kind: 'tool_result',
    step: 4,
    tool: { id: 'c1', name: 'run_command', state: 'done', command: 'go test', output: 'ok' },
  },
  lead_message: { message: 'Fix it', by: 'lead', tier: 'standard' },
  route: {
    accountId: 'a1',
    accountLabel: 'agy 1',
    model: 'gemini',
    reason: 'priority',
    continuing: false,
  },
  worker_prompt: { length: 4210, preview: 'You are a worker…' },
  attempt_end: { status: 'ok' },
  turn_end: { status: 'ok', response: 'Done', files: ['a.go'] },
  quota_exhausted: { accountId: 'a1', group: 'pro', error: 'limit' },
  auth_failed: { accountId: 'a1', error: 'expired' },
  waiting_quota: { until: '2026-11-06T17:00:00.000Z' },
  retry: { delayMs: 2000, error: 'timeout' },
  handoff: { from: 'a1', to: 'a2', reason: 'quota' },
  reattach: { accountId: 'a1', offset: 120 },
  resumed: { accountId: 'a1' },
  attention: { state: 'question', reason: 'question', who: 'lead', since: base.ts },
  deps: { state: 'ready', after: ['t0'] },
  takeover: { state: 'end', accountId: 'a1', durationMs: 5000, exitCode: 0 },
  handback: { turn: 2, response: 'I fixed it' },
  review: { verdict: 'accepted' },
  cancel: {},
  work_at_risk: { files: ['a.go'] },
  restored: { files: ['a.go'], backup: 't1-restore-1' },
  snapshot_truncated: { snapshots: ['end'], maxFiles: 3000, maxBytes: 268435456 },
  gate: { phase: 'start', steps: ['lane', 'go test ./...'] },
  lane: { kind: 'violation', paths: ['src/'], files: ['README.md'] },
  verify: { phase: 'start', childId: 't2' },
  error: { message: 'boom' },
}

/** Checks one fixture file's content: a valid event of the type it is named after. */
function checkFixture(type: string, value: unknown): string {
  const problem = taskEventProblem(value)
  if (problem) return problem
  return (value as { type: string }).type === type ? '' : 'type does not match the file name'
}

describe('TaskEvent guard', () => {
  it('has a fixture for every type and accepts each', () => {
    expect(Object.keys(FIXTURES).sort()).toEqual([...TASK_EVENT_TYPES].sort())
    for (const type of TASK_EVENT_TYPES) {
      expect(checkFixture(type, { ...base, type, data: FIXTURES[type] }), type).toBe('')
    }
  })

  it('rejects an event missing any required data key', () => {
    for (const type of TASK_EVENT_TYPES) {
      for (const [key] of EVENT_REQUIRED[type]) {
        const data = { ...FIXTURES[type] }
        delete data[key]
        expect(taskEventProblem({ ...base, type, data }), `${type}.${key}`).toBe(`data.${key}`)
      }
    }
  })

  it('checks the envelope, worker kinds and set attention', () => {
    const ok = { ...base, type: 'cancel', data: {} }
    expect(isTaskEvent(ok)).toBe(true)
    expect(taskEventProblem({ ...ok, seq: 0 })).toBe('seq')
    expect(taskEventProblem({ ...ok, taskId: 1 })).toBe('taskId')
    expect(taskEventProblem({ ...ok, data: null })).toBe('data')
    expect(taskEventProblem({ ...base, type: 'worker', data: { kind: 'telepathy' } })).toBe(
      'data.kind',
    )
    expect(isTaskEvent({ ...base, type: 'attention', data: { state: 'cleared' } })).toBe(true)
    expect(taskEventProblem({ ...base, type: 'attention', data: { state: 'blocked' } })).toBe(
      'data.reason',
    )
    expect(taskEventProblem({ ...base, type: 'meeting', data: {} })).toBe('unknown type meeting')
  })

  it('drops unknown types and logs each one once', () => {
    const log = vi.fn()
    const parse = createEventParser(log)
    const gate = { ...base, type: 'gate', data: { state: 'red' } }
    expect(parse(gate)).toBeNull()
    expect(parse({ ...gate, seq: 4 })).toBeNull()
    expect(parse({ ...base, type: 'lane', data: {} })).toBeNull()
    expect(log).toHaveBeenCalledTimes(2)
    const valid = { ...base, type: 'error', data: { message: 'x' } }
    expect(parse(valid)).toBe(valid)
  })
})
