import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'

import type { TasksApi } from '@/api'
import type { Consult, TaskDetail } from '@/api/types'
import { flush, setupApp } from '@/test/app'
import { iso, makeDetail, makeTask } from '@/test/fixtures'
import { useTasksStore } from '@/stores/tasks'

import { revision, summaryOf, useTaskDetail } from './useTaskDetail'

const consult = (overrides: Partial<Consult> = {}): Consult => ({
  id: 'c1',
  member: 'rev',
  rank: 'peer',
  runTaskId: 't2',
  status: 'running',
  findings: [],
  required: [],
  at: iso(),
  answeredObjection: null,
  ...overrides,
})

describe('revision', () => {
  it('changes when a consult ends or its objection is answered, and nothing else moved (N3)', () => {
    const running = makeTask({
      status: 'done',
      consultState: { total: 1, running: 1, objections: 0 },
    })
    const objected = { ...running, consultState: { total: 1, running: 0, objections: 1 } }
    const answered = { ...running, consultState: { total: 1, running: 0, objections: 0 } }
    const none = makeTask({ status: 'done' })
    expect(revision(objected)).not.toBe(revision(running))
    expect(revision(answered)).not.toBe(revision(objected))
    expect(revision(running)).not.toBe(revision(none))
  })
})

describe('useTaskDetail', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('reads the record again when a task.updated says a consult ended (N3)', async () => {
    const { plugins } = await setupApp()
    const before = makeDetail({
      status: 'done',
      consultState: { total: 1, running: 1, objections: 0 },
      consults: [consult()],
    })
    const after = makeDetail({
      status: 'done',
      updatedAt: iso(5000),
      consultState: { total: 1, running: 0, objections: 1 },
      consults: [consult({ status: 'done', verdict: 'object', required: ['set it to three'] })],
    })
    const get = vi
      .fn<(id: string) => Promise<TaskDetail>>()
      .mockResolvedValueOnce(before)
      .mockResolvedValueOnce(after)
    const api = { get } as unknown as TasksApi
    const holder: { state?: ReturnType<typeof useTaskDetail> } = {}
    mount(
      defineComponent({
        setup() {
          holder.state = useTaskDetail(() => 't1', api)
          return () => null
        },
      }),
      { global: { plugins } },
    )
    await flush()
    expect(get).toHaveBeenCalledTimes(1)
    expect(holder.state?.detail.value?.consults[0]?.status).toBe('running')

    // The SSE task.updated: only the summary's consult state differs.
    useTasksStore().upsertIfNewer(summaryOf(after))
    await flush()
    await vi.advanceTimersByTimeAsync(1100)
    await flush()
    expect(get).toHaveBeenCalledTimes(2)
    expect(holder.state?.detail.value?.consults[0]?.verdict).toBe('object')
  })
})
