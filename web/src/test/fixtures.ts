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

/**
 * `n` accounts as a busy install has them (the Accounts list at scale): Antigravity, Claude Code and Codex in turn,
 * with the quota groups each backend reports (Antigravity's three, Claude's sub-limits), some busy, resting, signed
 * out or on an API key.
 */
export function makeAccountFleet(n: number): Account[] {
  const backends = ['agy', 'claude', 'codex'] as const
  const w = (window: string, remaining: number, hours: number) => ({
    window,
    remaining,
    resetAt: iso(hours * 3_600_000),
  })
  return Array.from({ length: n }, (_, i) => {
    const backend = backends[i % 3] as Account['backend']
    const left = ((i * 37) % 100) / 100
    const groups =
      backend === 'agy'
        ? [
            { name: 'Gemini Models', windows: [w('weekly', 0.9, 120), w('5h', left, 3)] },
            { name: 'Claude and GPT models', windows: [w('weekly', 0, 100), w('5h', 0, 2)] },
            { name: 'Gemini 4 Argon', windows: [w('weekly', 0, 140)] },
          ]
        : backend === 'claude'
          ? [
              { name: 'Claude', windows: [w('5h', left, 4), w('weekly', 0.7, 90)] },
              { name: 'Claude Opus', windows: [w('weekly', 0.5, 90)] },
            ]
          : [{ name: 'Codex', windows: [w('5h', left, 2), w('weekly', 0.6, 60)] }]
    const apiKey = backend !== 'agy' && i % 5 === 4
    return makeAccount({
      id: `acc-${i + 1}`,
      backend,
      kind: apiKey ? 'api-key' : 'isolated',
      label: `${backend} ${i + 1}`,
      priority: 20 - i,
      status: i % 7 === 6 ? 'needs_login' : 'ok',
      busy: i % 4 === 0 ? 1 : 0,
      resting: i % 9 === 8,
      ...(apiKey ? { provider: 'deepseek', keyHint: '…9f2c' } : {}),
      quota: apiKey ? { source: 'passive', groups: [] } : { source: 'active', groups },
    })
  })
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
    prices: [],
    spendSource: 'none',
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
