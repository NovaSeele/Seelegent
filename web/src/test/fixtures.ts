/** Builders for API data in tests: complete objects with sensible defaults, overridden per test. */
import type { TasksApi } from '@/api/tasks'
import type { TaskEvent, TaskEventType } from '@/api/events'
import type { Account, Health, Provider, TaskDetail, TaskSummary } from '@/api/types'

export const T0 = Date.parse('2026-11-06T10:00:00.000Z')

export const iso = (offsetMs = 0) => new Date(T0 + offsetMs).toISOString()

/** The machine-sized settings of a Health, all auto, on a 16 GB / 8-CPU Windows machine. */
export function makeHealthResources(): Pick<Health, 'resources' | 'machine'> {
  const auto = (value: number) => ({ value, source: 'auto' as const })
  return {
    resources: {
      minFreeMemoryMB: auto(2048),
      criticalFreeMemoryMB: auto(1024),
      minFreeCommitMB: auto(2457),
      testParallelism: auto(2),
      gateConcurrency: auto(1),
    },
    machine: { totalPhysMB: 16384, totalCommitMB: 24576, cpus: 8 },
  }
}

export function makeTask(overrides: Partial<TaskSummary> = {}): TaskSummary {
  return {
    id: 't1',
    title: 'Fix the login form',
    status: 'running',
    kind: 'normal',
    mode: 'edit',
    cwd: 'D:/Work/app',
    projectKey: 'd:/work/app',
    projectName: 'app',
    backend: 'agy',
    model: 'gemini-3.8-flash',
    tier: 'standard',
    accountId: 'a1',
    accountLabel: 'Work',
    profile: null,
    session: null,
    turnCount: 1,
    tokensUsed: 1200,
    filesChanged: 0,
    createdAt: iso(),
    updatedAt: iso(),
    attention: null,
    review: null,
    gate: null,
    spec: null,
    activeStep: null,
    after: [],
    afterMode: 'accepted',
    manual: false,
    lastEventSeq: 0,
    ...overrides,
  }
}

export function makeDetail(overrides: Partial<TaskDetail> = {}): TaskDetail {
  return {
    ...makeTask(),
    prompt: 'Fix the login form validation.',
    turns: [],
    fileOverlaps: [],
    proof: null,
    consults: [],
    ...overrides,
  }
}

export function makeEvent(
  taskId: string,
  seq: number,
  data: Record<string, unknown> = { kind: 'message', text: `m${seq}` },
  type: TaskEventType = 'worker',
): TaskEvent {
  return { seq, ts: iso(seq * 1000), taskId, turn: 1, attempt: 1, type, data } as TaskEvent
}

export function makeAccount(overrides: Partial<Account> = {}): Account {
  return {
    id: 'a1',
    backend: 'agy',
    kind: 'isolated',
    label: 'Work',
    enabled: true,
    priority: 0,
    status: 'ok',
    busy: 0,
    maxConcurrent: 2,
    resting: false,
    signingIn: false,
    quota: { source: 'active', groups: [] },
    models: [],
    createdAt: iso(),
    updatedAt: iso(),
    ...overrides,
  }
}

export function makeProvider(overrides: Partial<Provider> = {}): Provider {
  return {
    id: 'zai',
    name: 'Z.ai GLM',
    backend: 'claude',
    baseUrl: 'https://api.z.ai/api/anthropic',
    enabled: true,
    models: [],
    accounts: 0,
    preset: 'zai',
    official: false,
    allowedModels: '',
    tiers: { fast: '', standard: '', strong: '', max: '' },
    createdAt: iso(),
    updatedAt: iso(),
    ...overrides,
  }
}

type Fn<K extends keyof TasksApi> = TasksApi[K]

/** A TasksApi whose methods reject until a test gives them an implementation. */
export function fakeTasksApi(overrides: Partial<TasksApi> = {}): TasksApi {
  const missing = (name: string) => () => Promise.reject(new Error(`unexpected call ${name}`))
  const base = {
    bootState: missing('bootState') as Fn<'bootState'>,
    projects: missing('projects') as Fn<'projects'>,
    list: missing('list') as Fn<'list'>,
    get: missing('get') as Fn<'get'>,
    events: missing('events') as Fn<'events'>,
    prompt: missing('prompt') as Fn<'prompt'>,
    followup: missing('followup') as Fn<'followup'>,
    review: missing('review') as Fn<'review'>,
    cancel: missing('cancel') as Fn<'cancel'>,
    answer: missing('answer') as Fn<'answer'>,
    dismissAttention: missing('dismissAttention') as Fn<'dismissAttention'>,
    takeover: missing('takeover') as Fn<'takeover'>,
  }
  return { ...base, ...overrides }
}
