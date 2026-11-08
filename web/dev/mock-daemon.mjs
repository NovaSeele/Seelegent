#!/usr/bin/env node
/**
 * Mock Seelegent daemon for dashboard development and design review. Not shipped: Vite never bundles dev/.
 *
 *   pnpm mock          # http://127.0.0.1:47831/api/v1, the port `pnpm dev` proxies /api to
 *   pnpm dev           # http://localhost:5173
 *
 * Serves the /api/v1 endpoints the Watch and Tasks views use (contracts/api-v1.md) from in-memory sample data:
 * several projects (one a monorepo package), every task status, a running task that streams worker events (text
 * every 250 ms, a tool step every second), a task asking a question, a failed one with an error, tasks with git
 * changes and parsed diffs, and a new task every 90 s; the Accounts, Usage, Settings and Roles routes live in
 * mock-admin.mjs. Writes (followup, answer, review, cancel, takeover, commit, suggest-message, attention
 * dismiss) change the data and broadcast SSE frames.
 *
 * Contract behaviour it honours, the way the real daemon does it (internal/httpapi): the Host check (only
 * 127.0.0.1, localhost or [::1] with the mock's own port, else 421, on every path), X-Seelegent and same-origin
 * checks on writes (403), the error shape, unknown body fields rejected, Cache-Control: no-store, SSE ids
 * `<instance>.<seq>` on every frame with resume from a ring of 2048 frames (`reset` restart for another
 * instance's id, too_old outside the ring), hello first (its id is the current one, or the client's on a resume;
 * a reset carries the current id), ping every 25 s, events read with after/limit or tail (never both), prompt
 * without attempt = the latest attempt, cancel of a finished task = 200 and no change, a git timeout as
 * 502 upstream with details.source "git", git paths relative to the repo root (Turn.files relative to the cwd).
 *
 * Env: MOCK_PORT (47831), MOCK_TICK_MS (250), MOCK_OLD_TASKS (140 finished tasks for paging), MOCK_NO_HOOKS=1
 * (every /hooks route answers 501 not_implemented, like the daemon until hooks land), MOCK_DEV_ORIGIN (default
 * http://localhost:5173; empty turns it off): one extra loopback http origin accepted by the Host and Origin
 * checks, like `seelegent daemon --dev-origin`, because the Vite proxy keeps the browser's Host and Origin.
 * Resources: MOCK_OS (Health.os, default this machine's: windows, linux, darwin), MOCK_LOW_MEMORY=1 (the memory
 * watchdog holds new starts from the start) or MOCK_LOW_MEMORY=flip (it turns on and off every 30 s, with a
 * resources.changed frame each time); Health.memory is this machine's free memory (commit only on Windows).
 */
import { randomBytes } from 'node:crypto'
import { createServer } from 'node:http'
import { availableParallelism, freemem, totalmem } from 'node:os'

import { createAdminRoutes } from './mock-admin.mjs'

const PORT = Number(process.env.MOCK_PORT ?? 47831)
const TICK_MS = Number(process.env.MOCK_TICK_MS ?? 250)
const OLD_TASKS = Number(process.env.MOCK_OLD_TASKS ?? 140)
const RING = 2048
const DEV_ORIGIN = devOrigin(process.env.MOCK_DEV_ORIGIN ?? 'http://localhost:5173')
const INSTANCE = randomBytes(4).toString('hex')
const BUILD = 'mock-build'
const GOOS = { win32: 'windows', darwin: 'darwin', linux: 'linux' }
const OS = process.env.MOCK_OS || GOOS[process.platform] || process.platform
const LOW_MEMORY = process.env.MOCK_LOW_MEMORY ?? ''
const STARTED = new Date()
const MIN = 60_000
const HOUR = 60 * MIN

// ---------------------------------------------------------------------------------------------------------------
// Helpers

let seed = 42
const rand = () => (seed = (seed * 1103515245 + 12345) % 2147483648) / 2147483648
const pick = (list) => list[Math.floor(rand() * list.length)]
const now = () => new Date().toISOString()
const ago = (ms) => new Date(Date.now() - ms).toISOString()
const hex = (n) =>
  randomBytes(Math.ceil(n / 2))
    .toString('hex')
    .slice(0, n)

// ---------------------------------------------------------------------------------------------------------------
// Static data

const projects = [
  project('D:/Work/shop-api', 'shop-api', 'D:/Work/shop-api'),
  project('D:/Work/mobile-app', 'mobile-app', 'D:/Work/mobile-app'),
  project('D:/Work/storefront/packages/web', 'storefront-web', 'D:/Work/storefront'),
  project('D:/Work/Seelegent', 'Seelegent', 'D:/Work/Seelegent'),
  project('C:/Users/dev/notes', 'notes'),
]

function project(cwd, name, repoRoot) {
  return { key: cwd.toLowerCase(), cwd, name, repoRoot }
}

const projectOf = (key) => projects.find((p) => p.key === key) ?? projects[0]

/** The cwd relative to its repo root, with a trailing '/' ('' at the root). */
function cwdPrefix(p) {
  if (!p.repoRoot || p.cwd === p.repoRoot) return ''
  return `${p.cwd.slice(p.repoRoot.length + 1)}/`
}

const accounts = [
  account('acc-work', 'agy', 'isolated', 'Work Pro', 10, 'ok', 1, {
    plan: 'Google AI Pro',
    groups: [
      group('gemini-pro', [win('5h', 0.62, 3 * HOUR), win('weekly', 0.8, 4 * 24 * HOUR)]),
      group('gemini-flash', [win('5h', 0.91, 3 * HOUR)]),
    ],
  }),
  account('acc-personal', 'agy', 'isolated', 'Personal', 5, 'ok', 0, {
    plan: 'Google AI Pro',
    groups: [
      group('gemini-pro', [win('5h', 0.18, 50 * MIN)]),
      group('gemini-flash', [win('5h', 0.74, 50 * MIN)]),
    ],
  }),
  account(
    'acc-claude',
    'claude',
    'system',
    'Claude Max',
    3,
    'ok',
    0,
    {
      plan: 'Max',
      groups: [group('opus', [win('5h', 0, 2 * HOUR)])],
      exhaustedUntil: ago(-2 * HOUR),
      exhaustedReason: 'Usage limit reached.',
    },
    { resting: true, restingUntil: ago(-2 * HOUR) },
  ),
  account('acc-backup', 'agy', 'isolated', 'Backup login', 2, 'error', 0, {
    groups: [group('gemini-pro', [win('5h', 0.55, 2 * HOUR)])],
    error: { kind: 'unavailable', message: 'The quota service did not answer.', at: ago(6 * MIN) },
  }),
  account(
    'acc-codex',
    'codex',
    'api-key',
    'Codex API',
    1,
    'ok',
    0,
    { source: 'passive', fetchedAt: ago(25 * MIN) },
    { keyHint: '…9f2c', provider: 'openai' },
  ),
  account(
    'acc-deepseek',
    'claude',
    'api-key',
    'DeepSeek',
    0,
    'no_key',
    0,
    { source: 'passive' },
    {
      provider: 'deepseek',
    },
  ),
  account('acc-team', 'claude', 'isolated', 'Team seat', 4, 'needs_login', 0, {
    error: { kind: 'token_expired', message: 'The sign-in expired.', at: ago(2 * HOUR) },
  }),
  account('acc-free', 'claude', 'isolated', 'Free plan', 0, 'no_plan', 0, {
    plan: 'Free',
    noPlan: true,
    noPlanReason: 'The Free plan cannot use Claude Code.',
  }),
  account('acc-twin', 'agy', 'isolated', 'Work (copy)', 1, 'twin', 0, {}, { twinOf: 'acc-work' }),
  account('acc-fresh', 'agy', 'isolated', 'New laptop', 0, 'new', 0, { fetchedAt: undefined }),
  account('acc-old', 'codex', 'isolated', 'Old seat', -5, 'disabled', 0, {
    plan: 'Plus',
    groups: [group('codex', [win('5h', 0.4, 4 * HOUR), win('weekly', 0.7, 3 * 24 * HOUR)])],
  }),
]
accounts[0].email = 'work.pro@example.test'
accounts[1].email = 'me@example.test'

/** Account per contract §4: the pool status and runtime, and a quota snapshot. */
function account(id, backend, kind, label, priority, status, busy, quotaSnapshot, extra = {}) {
  return {
    id,
    backend,
    kind,
    label,
    enabled: status !== 'disabled',
    priority,
    status,
    busy,
    maxConcurrent: 2,
    resting: false,
    signingIn: false,
    quota: { source: 'active', fetchedAt: ago(4 * MIN), groups: [], ...quotaSnapshot },
    models: backend === 'agy' ? ['gemini-3.8-flash', 'gemini-3.8-pro'] : [],
    createdAt: ago(40 * 24 * HOUR),
    updatedAt: ago(4 * MIN),
    ...extra,
  }
}

function group(name, windows) {
  return { name, windows }
}

function win(window, remaining, resetsIn) {
  return { window, remaining, resetAt: ago(-resetsIn) }
}

const backends = [
  backend('agy', 'Antigravity', 5, 1),
  backend('claude', 'Claude Code', 4, 0),
  backend('codex', 'Codex', 2, 0),
]

function backend(id, name, count, active) {
  return {
    id,
    name,
    enabled: true,
    installed: true,
    version: '1.4.2',
    path: `C:/Program Files/${name}/${id}.exe`,
    accounts: count,
    active,
    capabilities: { resume: true, apiKey: id !== 'agy', takeover: true, listModels: id === 'agy' },
  }
}

function tiers(map) {
  return {
    enabled: true,
    allowedModels: '',
    tiers: { fast: '', standard: '', strong: '', max: '', ...map },
  }
}

const settings = {
  port: PORT,
  defaultTier: 'standard',
  backends: {
    agy: tiers({
      fast: 'gemini-3.8-flash',
      standard: 'gemini-3.8-flash',
      strong: 'gemini-3.8-pro',
    }),
    codex: { ...tiers({ standard: 'gpt-5.5-codex' }), sandbox: 'workspace-write' },
    claude: tiers({ strong: 'claude-sonnet-5', max: 'claude-opus-5' }),
  },
  maxConcurrentPerAccount: 2,
  quotaRefreshMinutes: 10,
  minRemaining: 0.05,
  passiveCooldownMinutes: 30,
  usageHistory: true,
  taskTimeoutMinutes: 60,
  linkIntoProfiles: [],
  stallMinutes: 8,
  stallToolMinutes: 20,
  loopRepeat: 4,
  attentionDecayHours: 24,
  gates: true,
  gateAutoChecks: true,
  gateTimeoutSec: 600,
  gateMaxAutoFixes: 2,
  gateSettleMinutes: 2,
  gateSettlePollMs: 2000,
  gateOutputTailBytes: 8192,
  hygiene: true,
  lanes: 'warn',
  laneHoldHours: 4,
  verifier: false,
  verifierTier: 'fast',
  gitGuard: true,
  codeGraph: false,
  snapshotRetentionDays: 14,
  contentionWindowHours: 24,
  githubPollMinutes: 0,
  automationsEnabled: false,
  workerPriority: 'low',
  gatePriority: 'low',
  maxConcurrentWorkers: 0,
  // gateConcurrency, minFreeMemoryMB, minFreeCommitMB, criticalFreeMemoryMB and testParallelism are auto: absent
  // until set (Health.resources has the values in use).
  workerMemoryLimitMB: 0,
  workerCPURatePercent: 0,
  daemonMemoryLimitMB: 0,
}

// ---------------------------------------------------------------------------------------------------------------
// Tasks, event logs, git

/** id -> { summary, prompt, error?, turns, prompts: Map<"turn.attempt", string>, events, git, sim } */
const tasks = new Map()
let taskCounter = 0

const newId = () => `t2611${String(++taskCounter).padStart(3, '0')}-${hex(4)}`

function makeTask(spec) {
  const p = projectOf(spec.project)
  const acc = accounts.find((a) => a.id === spec.account) ?? accounts[0]
  const created = spec.createdAt ?? ago(spec.age ?? 30 * MIN)
  const summary = {
    id: spec.id ?? newId(),
    title: spec.title,
    status: spec.status,
    kind: spec.kind ?? 'normal',
    mode: 'edit',
    cwd: p.cwd,
    projectKey: p.key,
    projectName: p.name,
    backend: acc.backend,
    model: spec.model ?? (acc.backend === 'agy' ? 'gemini-3.8-flash' : 'claude-sonnet-5'),
    tier: spec.tier ?? 'standard',
    role: spec.role,
    profile: null,
    session: null,
    turnCount: 0,
    tokensUsed: 0,
    filesChanged: 0,
    createdAt: created,
    updatedAt: created,
    attention: spec.attention ?? null,
    review: spec.review ?? null,
    gate: spec.gate ?? null,
    spec: spec.spec ?? null,
    activeStep: null,
    after: spec.after ?? [],
    afterMode: 'accepted',
    manual: !!spec.manual,
    lastEventSeq: 0,
  }
  if (!spec.unassigned) {
    summary.accountId = acc.id
    summary.accountLabel = acc.label
  }
  if (spec.waitingUntil) summary.waitingUntil = spec.waitingUntil
  if (spec.depsReason) summary.depsReason = spec.depsReason
  if (summary.attention) summary.needsSince = summary.attention.since
  const task = {
    summary,
    prompt: spec.prompt ?? `${spec.title}.\n\nKeep the public API unchanged and add tests.`,
    error: spec.error,
    turns: [],
    prompts: new Map(),
    events: [],
    git: { files: (spec.files ?? []).map((f) => ({ ...f })), preexisting: [], suggestion: null },
    sim: null,
  }
  tasks.set(summary.id, task)
  return task
}

function pushEvent(task, type, data, emit = true) {
  const s = task.summary
  const turn = task.turns.at(-1)
  // Seeded turns run on their own clock from the task's creation, a few seconds per event, so the Graph has a
  // realistic time axis; live events use the wall clock.
  let ts = now()
  if (!emit && task.seedClock !== undefined) {
    task.seedClock += 1500 + Math.floor(rand() * 7000)
    ts = new Date(Math.min(task.seedClock, Date.now())).toISOString()
  }
  const event = { seq: task.events.length + 1, ts, taskId: s.id, type, data }
  if (turn) {
    event.turn = turn.n
    event.attempt = Math.max(1, turn.attempts.length)
  }
  task.events.push(event)
  s.lastEventSeq = event.seq
  s.lastEventAt = event.ts
  if (emit) broadcast('task.event', event)
  return event
}

function touch(task, emit = true) {
  task.summary.updatedAt = now()
  if (emit) broadcast('task.updated', task.summary)
}

function brief(task, message, n) {
  const s = task.summary
  return [
    `# Task ${s.id} — turn ${n}`,
    '',
    `You are a worker in ${s.cwd}. Do exactly what the lead asks, run the tests, then report.`,
    '',
    '## Request',
    '',
    message,
    '',
    '## Rules',
    '',
    '- Edit only files inside the working directory.',
    '- Never run git commit, push or reset; the lead commits.',
    '- When you are blocked, end your report with a "## Blocked" section that asks one question.',
    '',
    '## Project memory',
    '',
    ...Array.from(
      { length: 30 },
      (_, i) => `- Convention ${i + 1}: keep functions small and tested.`,
    ),
  ].join('\n')
}

function startTurn(task, message, by = 'lead', emit = false) {
  const s = task.summary
  const n = task.turns.length + 1
  const acc = accounts.find((a) => a.id === s.accountId)
  const turn = {
    n,
    by,
    message,
    status: 'running',
    model: s.model,
    tier: s.tier,
    startedAt: now(),
    files: [],
    commands: [],
    blockedCommands: [],
    deniedActions: [],
    usage: null,
    attempts: [
      {
        accountId: s.accountId ?? '',
        accountLabel: s.accountLabel,
        model: s.model,
        startedAt: now(),
        status: 'running',
        usage: null,
      },
    ],
    git: null,
  }
  task.turns.push(turn)
  s.turnCount = n
  pushEvent(task, 'lead_message', { message, by, model: s.model, tier: s.tier }, emit)
  if (acc) {
    pushEvent(
      task,
      'route',
      {
        accountId: acc.id,
        accountLabel: acc.label,
        model: s.model,
        reason: n === 1 ? 'highest priority with quota' : 'same account, resuming the conversation',
        continuing: n > 1,
        remaining: acc.quota.groups[0]?.windows[0]?.remaining,
      },
      emit,
    )
  }
  const text = brief(task, message, n)
  task.prompts.set(`${n}.1`, text)
  pushEvent(task, 'worker_prompt', { length: text.length, preview: text.slice(0, 500) }, emit)
  pushEvent(
    task,
    'worker',
    { kind: 'session', session: { conversationId: `conv-${hex(8)}`, model: s.model } },
    emit,
  )
  return turn
}

/** Turn.files are relative to the cwd (display only); git paths are relative to the repo root. */
function cwdRelative(task, rootPath) {
  const prefix = cwdPrefix(projectOf(task.summary.projectKey))
  return rootPath.startsWith(prefix) ? rootPath.slice(prefix.length) : `../${rootPath}`
}

function endTurn(task, status, response, emit = false) {
  const s = task.summary
  const turn = task.turns.at(-1)
  if (!turn) return
  const usage = {
    inputTokens: 18000 + Math.floor(rand() * 9000),
    outputTokens: 2100 + Math.floor(rand() * 1500),
    cachedTokens: 9000,
    totalTokens: 0,
  }
  usage.totalTokens = usage.inputTokens + usage.outputTokens
  turn.status = status
  turn.endedAt = now()
  turn.response = response
  turn.usage = usage
  const attempt = turn.attempts.at(-1)
  if (attempt) {
    attempt.status = status === 'ok' ? 'ok' : status === 'cancelled' ? 'cancelled' : 'error'
    attempt.endedAt = now()
    attempt.usage = usage
  }
  const changed = task.git.files.filter((f) => f.turn === turn.n).map((f) => f.path)
  turn.files = changed.map((path) => cwdRelative(task, path))
  turn.git = { changed, preexisting: [] }
  s.tokensUsed += usage.totalTokens
  s.filesChanged = task.git.files.length
  s.activeStep = null
  pushEvent(
    task,
    'worker',
    {
      kind: 'report',
      report: {
        status: status === 'ok' ? 'success' : 'error',
        response,
        usage,
        durationMs: 42_000,
      },
    },
    emit,
  )
  pushEvent(task, 'attempt_end', { status: attempt?.status ?? 'ok' }, emit)
  pushEvent(task, 'turn_end', { status, response, files: turn.files, usage }, emit)
}

// The worker script. Steps are relative to the task's cwd; `msg` streams as text, `run` is a shell call.
const SCRIPT = [
  {
    msg: 'I will start by reading the handler and its tests to see how requests reach the service.',
  },
  { read: 'internal/checkout/handler.go' },
  { read: 'internal/checkout/handler_test.go' },
  {
    run: 'rg -n "RateLimit" internal',
    out: 'internal/config/config.go:41:\tRateLimit RateLimitConfig `json:"rateLimit"`',
  },
  {
    msg: 'There is already a `RateLimitConfig` in the config. I will add a token bucket per client key:\n\n```go\ntype bucket struct {\n\ttokens float64\n\tlast   time.Time\n}\n```\n\nSee https://pkg.go.dev/golang.org/x/time/rate for the reference design.',
  },
  { edit: 'internal/checkout/ratelimit.go' },
  { edit: 'internal/checkout/handler.go' },
  { run: 'go build ./...', out: '' },
  { run: 'go test ./internal/checkout/...', out: 'ok  \tshop-api/internal/checkout\t0.412s' },
  { usage: true },
  { edit: 'internal/checkout/ratelimit_test.go' },
  {
    run: 'go test -race ./internal/checkout/...',
    out: '--- FAIL: TestBucketRefill (0.00s)\n    ratelimit_test.go:48: got 9 tokens, want 10\nFAIL\nFAIL\tshop-api/internal/checkout\t0.188s',
    fail: true,
  },
  {
    msg: 'The refill rounds down. I will compute the elapsed time in float seconds instead of whole seconds.',
  },
  { edit: 'internal/checkout/ratelimit.go' },
  { run: 'go test -race ./internal/checkout/...', out: 'ok  \tshop-api/internal/checkout\t1.204s' },
  { usage: true },
]

/** Splits a message into the ~250 ms pieces a coalescing engine would store. */
function chunks(text, size = 6) {
  const words = text.split(/(?<=\s)/)
  const out = []
  for (let i = 0; i < words.length; i += size) out.push(words.slice(i, i + size).join(''))
  return out
}

function stepNumber(task) {
  const turn = task.turns.at(-1)
  return task.events.filter((e) => e.turn === turn?.n && e.type === 'worker').length
}

/** One whole step (seeded turns emit message text in a few pieces). */
function scriptStep(task, step, emit) {
  const s = task.summary
  const turn = task.turns.at(-1)
  const stepNo = stepNumber(task)
  const prefix = cwdPrefix(projectOf(s.projectKey))
  if (step.msg) {
    for (const text of chunks(step.msg, 12)) {
      pushEvent(task, 'worker', { kind: 'message', step: stepNo, text }, emit)
    }
    s.activeStep = { label: 'Thinking', since: now() }
  } else if (step.read || step.edit) {
    const path = step.read ?? step.edit
    const tool = step.read ? 'read_file' : 'edit_file'
    const id = `call-${hex(6)}`
    const abs = `${s.cwd}/${path}`
    pushEvent(
      task,
      'worker',
      {
        kind: 'tool_call',
        step: stepNo,
        tool: { id, name: tool, state: 'calling', input: { path: abs } },
      },
      emit,
    )
    pushEvent(
      task,
      'worker',
      { kind: 'file', step: stepNo, file: { path: abs, op: step.read ? 'read' : 'edit', tool } },
      emit,
    )
    pushEvent(
      task,
      'worker',
      {
        kind: 'tool_result',
        step: stepNo,
        tool: {
          id,
          name: tool,
          state: 'done',
          output: step.read ? `(${120 + Math.floor(rand() * 200)} lines)` : 'Applied 1 edit.',
          durationMs: 80 + Math.floor(rand() * 400),
        },
      },
      emit,
    )
    const rootPath = prefix + path
    if (step.edit && !task.git.files.some((f) => f.path === rootPath)) {
      task.git.files.push(gitFile(rootPath, task.git.files.length ? 'modified' : 'added', turn?.n))
    }
    s.activeStep = {
      label: `${step.read ? 'Reading' : 'Editing'} ${path.split('/').pop()}`,
      tool,
      since: now(),
    }
  } else if (step.run) {
    const id = `call-${hex(6)}`
    pushEvent(
      task,
      'worker',
      {
        kind: 'tool_call',
        step: stepNo,
        tool: { id, name: 'run_command', state: 'calling', command: step.run },
      },
      emit,
    )
    pushEvent(
      task,
      'worker',
      {
        kind: 'tool_result',
        step: stepNo,
        tool: {
          id,
          name: 'run_command',
          state: step.fail ? 'failed' : 'done',
          command: step.run,
          output: step.out,
          durationMs: 900 + Math.floor(rand() * 4000),
        },
      },
      emit,
    )
    turn?.commands.push(step.run)
    s.activeStep = { label: `Running ${step.run}`, tool: 'run_command', since: now() }
  } else if (step.usage) {
    const usage = {
      inputTokens: 4000 + Math.floor(rand() * 3000),
      outputTokens: 500 + Math.floor(rand() * 400),
      totalTokens: 0,
    }
    usage.totalTokens = usage.inputTokens + usage.outputTokens
    s.tokensUsed += usage.totalTokens
    pushEvent(task, 'worker', { kind: 'usage', step: stepNo, usage }, emit)
  }
  s.filesChanged = task.git.files.length
}

function gitFile(path, status, turn, extra = {}) {
  return {
    path,
    status,
    additions: status === 'deleted' ? 0 : 4 + Math.floor(rand() * 60),
    deletions: status === 'added' ? 0 : Math.floor(rand() * 20),
    turn,
    binary: path.endsWith('.png'),
    ...extra,
  }
}

/** Plays a whole finished turn into the log without broadcasting (seed data). */
function playTurn(
  task,
  message,
  steps,
  status = 'ok',
  response = 'Done. The change is in place and the tests pass.',
) {
  task.seedClock ??= Date.parse(task.summary.createdAt)
  startTurn(task, message)
  for (const step of steps) scriptStep(task, step, false)
  endTurn(task, status, response)
}

// ---------------------------------------------------------------------------------------------------------------
// Seed data

function seedData() {
  const oldTitles = [
    'Bump dependencies',
    'Fix flaky test',
    'Rename config keys',
    'Add logging',
    'Tidy README',
    'Remove dead code',
    'Speed up build',
    'Document API',
  ]
  for (let i = 0; i < OLD_TASKS; i++) {
    const failed = i % 7 === 0
    const cancelled = !failed && i % 9 === 0
    const t = makeTask({
      title: `${pick(oldTitles)} (#${OLD_TASKS - i})`,
      status: failed ? 'failed' : cancelled ? 'cancelled' : 'done',
      project: pick(projects).key,
      account: pick(accounts.slice(0, 2)).id,
      createdAt: ago((3 + i) * 7 * HOUR),
      // Old failures are reviewed (rejected) so they leave the "needs you" board.
      review: cancelled
        ? null
        : {
            verdict: failed || i % 11 === 0 ? 'rejected' : 'accepted',
            at: ago((3 + i) * 7 * HOUR - 20 * MIN),
          },
      error: failed ? 'The worker stopped: context window exceeded.' : undefined,
    })
    playTurn(t, t.summary.title, SCRIPT.slice(0, 3), failed ? 'failed' : 'ok')
    t.summary.updatedAt = new Date(Date.parse(t.summary.createdAt) + 25 * MIN).toISOString()
  }

  const [shop, mobile, storefront, seelegent, notes] = projects.map((p) => p.key)

  const helper = makeTask({
    title: 'Suggest a commit message',
    status: 'done',
    kind: 'commit-msg',
    project: shop,
    age: 2 * HOUR,
  })
  playTurn(helper, 'Write a commit message.', [{ msg: 'feat(checkout): add rate limiting' }])
  helper.summary.review = { verdict: 'accepted', at: ago(2 * HOUR) }

  const accepted = makeTask({
    title: 'Add pagination to the orders endpoint',
    status: 'done',
    project: shop,
    account: 'acc-personal',
    age: 6 * HOUR,
    review: { verdict: 'accepted', notes: 'Clean, thanks.', at: ago(5 * HOUR) },
    files: [gitFile('internal/orders/list.go', 'modified', 1)],
  })
  playTurn(accepted, accepted.summary.title, SCRIPT.slice(0, 9))
  pushEvent(accepted, 'review', { verdict: 'accepted', notes: 'Clean, thanks.' }, false)

  const rejected = makeTask({
    title: 'Dark mode for the profile screen',
    status: 'done',
    project: mobile,
    account: 'acc-personal',
    age: 5 * HOUR,
    review: {
      verdict: 'rejected',
      notes: 'Colours are hard-coded; use the theme tokens.',
      at: ago(4 * HOUR),
    },
    files: [gitFile('src/screens/Profile.tsx', 'modified', 1)],
  })
  playTurn(rejected, rejected.summary.title, SCRIPT.slice(4, 9))
  pushEvent(
    rejected,
    'review',
    { verdict: 'rejected', notes: 'Colours are hard-coded; use the theme tokens.' },
    false,
  )

  const cancelled = makeTask({
    title: 'Try a GraphQL gateway',
    status: 'cancelled',
    project: shop,
    age: 4 * HOUR,
  })
  playTurn(cancelled, cancelled.summary.title, SCRIPT.slice(0, 2), 'cancelled', '')
  pushEvent(cancelled, 'cancel', {}, false)

  const failed = makeTask({
    id: 't2611-failed',
    title: 'Upgrade the payment SDK to v5',
    status: 'failed',
    project: shop,
    account: 'acc-work',
    age: 3 * HOUR,
    error:
      'go test ./... failed: 3 tests in internal/payments (TestRefund, TestCapture, TestWebhookSignature).',
    gate: { state: 'red', phase: 'test', autoFixes: 2 },
    files: [
      gitFile('go.mod', 'modified', 1),
      gitFile('internal/payments/client.go', 'modified', 1),
    ],
  })
  playTurn(
    failed,
    failed.summary.title,
    SCRIPT.slice(8, 12),
    'failed',
    'The SDK changed its webhook signature format; three tests fail.',
  )
  pushEvent(failed, 'error', { message: failed.error }, false)

  const review = makeTask({
    id: 't2611-review',
    title: 'Validate e-mail addresses on sign-up',
    status: 'done',
    project: mobile,
    account: 'acc-work',
    age: 90 * MIN,
    role: 'frontend',
    files: [
      gitFile('src/forms/SignUp.tsx', 'modified', 1),
      gitFile('src/forms/validate.ts', 'added', 1),
      gitFile('src/forms/validate.test.ts', 'added', 1),
      gitFile('src/forms/legacyCheck.ts', 'deleted', 1),
      gitFile('src/assets/check.png', 'added', 1),
    ],
  })
  playTurn(
    review,
    review.summary.title,
    SCRIPT.slice(0, 10),
    'ok',
    'Added `validateEmail` with tests and wired it into the sign-up form.\n\n- Rejects addresses without a domain\n- Shows the error under the field\n\nRun `pnpm test` to check.',
  )
  review.summary.needsSince = review.turns.at(-1).endedAt

  // Monorepo: the cwd is a package; git paths are relative to the repo root.
  const mono = makeTask({
    id: 't2611-mono',
    title: 'Product page with dynamic route',
    status: 'done',
    project: storefront,
    account: 'acc-personal',
    age: 50 * MIN,
    files: [
      gitFile('packages/web/pages/[id].vue', 'added', 1),
      gitFile('packages/web/components/ProductCard.vue', 'renamed', 1, {
        oldPath: 'packages/web/components/Card.vue',
      }),
      gitFile('packages/shared/src/price.ts', 'modified', 1),
    ],
  })
  playTurn(mono, mono.summary.title, [
    SCRIPT[0],
    { read: 'pages/index.vue' },
    { edit: 'pages/[id].vue' },
  ])
  mono.summary.needsSince = mono.turns.at(-1).endedAt

  const question = makeTask({
    id: 't2611-question',
    title: 'Migrate the settings page to Vue 3',
    status: 'running',
    project: seelegent,
    account: 'acc-work',
    age: 40 * MIN,
    attention: {
      state: 'question',
      reason: 'question',
      who: 'lead',
      detail:
        'Should the old /settings URL redirect to /preferences, or keep working as it is?\n1. Redirect with 301\n2. Keep both URLs\n3. Remove the old URL',
      since: ago(12 * MIN),
    },
  })
  startTurn(question, question.summary.title)
  for (const step of SCRIPT.slice(0, 5)) scriptStep(question, step, false)
  pushEvent(question, 'attention', question.summary.attention, false)
  question.summary.activeStep = { label: 'Waiting for an answer', since: ago(12 * MIN) }

  const stalled = makeTask({
    title: 'Generate API client from OpenAPI',
    status: 'running',
    project: mobile,
    account: 'acc-personal',
    age: 70 * MIN,
    attention: {
      state: 'stalled',
      reason: 'stalled',
      who: 'lead',
      detail: 'No output for 14 minutes while running `pnpm openapi`.',
      since: ago(14 * MIN),
    },
  })
  startTurn(stalled, stalled.summary.title)
  for (const step of SCRIPT.slice(0, 4)) scriptStep(stalled, step, false)
  stalled.summary.activeStep = {
    label: 'Running pnpm openapi',
    tool: 'run_command',
    since: ago(14 * MIN),
  }

  // Waits for free memory (status waiting_quota, waiting "memory"), past the 10 min of a resources_wait flag.
  const memoryWait = makeTask({
    title: 'Run the full integration suite',
    status: 'waiting_quota',
    project: shop,
    account: 'acc-work',
    age: 14 * MIN,
    attention: {
      state: 'info',
      reason: 'resources_wait',
      who: 'user',
      detail: 'Waiting for resources for 12 minutes: free 1500 MB < threshold 2048 MB.',
      params: { minutes: 12, availableMB: 1500, needMB: 2048 },
      since: ago(2 * MIN),
    },
  })
  memoryWait.summary.waiting = 'memory'
  startTurn(memoryWait, memoryWait.summary.title)
  pushEvent(
    memoryWait,
    'waiting_quota',
    { reason: 'memory', availableMB: 1820, needMB: 2048 },
    false,
  )
  pushEvent(
    memoryWait,
    'waiting_quota',
    { reason: 'memory', availableMB: 1500, needMB: 2048 },
    false,
  )

  // A gate waiting for a slot (gateConcurrency 2), told again when its place in line moved.
  const gateQueued = makeTask({
    title: 'Add rate limiting to the public API',
    status: 'running',
    project: shop,
    account: 'acc-work',
    age: 9 * MIN,
    gate: { state: 'running', phase: 'queued', wait: 'slot', autoFixes: 0, turn: 1 },
  })
  playTurn(gateQueued, gateQueued.summary.title, SCRIPT.slice(0, 3), 'ok', 'Added the limiter.')
  pushEvent(gateQueued, 'gate', { phase: 'queued', wait: 'slot', position: 3, limit: 2 }, false)
  pushEvent(gateQueued, 'gate', { phase: 'queued', wait: 'slot', position: 2, limit: 2 }, false)

  const quotaWait = makeTask({
    title: 'Refactor the billing service',
    status: 'waiting_quota',
    project: shop,
    account: 'acc-claude',
    age: 25 * MIN,
    waitingUntil: ago(-25 * MIN),
  })
  startTurn(quotaWait, quotaWait.summary.title)
  pushEvent(
    quotaWait,
    'quota_exhausted',
    {
      accountId: 'acc-claude',
      group: 'opus',
      until: quotaWait.summary.waitingUntil,
      error: 'Usage limit reached.',
    },
    false,
  )
  pushEvent(quotaWait, 'waiting_quota', { until: quotaWait.summary.waitingUntil }, false)

  makeTask({
    title: 'Translate onboarding screens to Japanese',
    status: 'queued',
    project: mobile,
    age: 2 * MIN,
    unassigned: true,
  })

  makeTask({
    title: 'Release notes for the sign-up change',
    status: 'waiting_deps',
    project: mobile,
    age: 60 * MIN,
    after: [review.summary.id],
    unassigned: true,
    depsReason: 'Waits until the e-mail validation task is accepted.',
  })
  const blockedDeps = makeTask({
    title: 'Deploy the payment SDK upgrade',
    status: 'blocked_deps',
    project: shop,
    age: 2 * HOUR,
    after: [failed.summary.id],
    unassigned: true,
    depsReason: 'The earlier task failed.',
  })
  pushEvent(
    blockedDeps,
    'deps',
    { state: 'blocked', after: [failed.summary.id], reason: 'The earlier task failed.' },
    false,
  )
  // Its first status read fails, so the view shows a read error with Retry (never "not a repository").
  blockedDeps.statusFailures = 1

  const manual = makeTask({
    title: 'Debug the flaky upload test',
    status: 'running',
    project: seelegent,
    account: 'acc-personal',
    age: 20 * MIN,
    manual: true,
  })
  startTurn(manual, manual.summary.title)
  for (const step of SCRIPT.slice(0, 3)) scriptStep(manual, step, false)
  pushEvent(manual, 'takeover', { state: 'start', accountId: 'acc-personal' }, false)

  const note = makeTask({
    title: 'Summarise meeting notes into a todo list',
    status: 'done',
    project: notes,
    account: 'acc-personal',
    age: 3 * HOUR,
    review: { verdict: 'accepted', at: ago(150 * MIN) },
  })
  playTurn(note, note.summary.title, [{ msg: 'Here is the list.' }])

  // The always-running task.
  const live = makeTask({
    id: 't2611-live',
    title: 'Add rate limiting to the checkout API',
    status: 'running',
    project: shop,
    account: 'acc-work',
    age: 8 * MIN,
    spec: { dir: 'specs/004-rate-limit', taskIds: ['T012', 'T013'] },
  })
  playTurn(live, live.summary.title, SCRIPT, 'ok', 'First pass done.')
  startTurn(live, 'Also cover the admin endpoints and keep the limits configurable per route.')
  live.sim = { kind: 'forever', step: 0 }
  live.summary.updatedAt = now()
}

// ---------------------------------------------------------------------------------------------------------------
// Simulation: one tick every TICK_MS. A message step sends one text piece per tick (the engine's coalescing);
// other steps run once every 4 ticks.

function simStep(task) {
  const sim = task.sim
  const step = SCRIPT[sim.step % SCRIPT.length]
  if (step.msg) {
    // Every piece of one message carries the same step (the engine coalesces deltas per step).
    sim.pieces ??= chunks(step.msg)
    sim.msgStep ??= stepNumber(task)
    const text = sim.pieces.shift()
    pushEvent(task, 'worker', { kind: 'message', step: sim.msgStep, text })
    task.summary.activeStep = { label: 'Thinking', since: now() }
    if (sim.pieces.length) return
    sim.pieces = undefined
    sim.msgStep = undefined
  } else {
    sim.wait = (sim.wait ?? 0) + 1
    if (sim.wait < 4) return
    sim.wait = 0
    scriptStep(task, step, true)
  }
  sim.step++
}

let ticks = 0
function tick() {
  ticks++
  for (const task of tasks.values()) {
    const sim = task.sim
    if (!sim) continue
    if (sim.kind === 'forever' || sim.kind === 'turn') {
      const before = task.events.length
      simStep(task)
      if (sim.kind === 'turn' && sim.step >= sim.steps) finishSimTurn(task)
      // Summaries are coalesced by the daemon (100 ms); one per second is plenty here.
      else if (task.events.length !== before && ticks % 4 === 0) touch(task)
    } else if (sim.kind === 'takeover' && Date.now() >= sim.until) {
      task.sim = null
      task.summary.manual = false
      pushEvent(task, 'takeover', {
        state: 'end',
        accountId: task.summary.accountId,
        durationMs: 15_000,
        exitCode: 0,
      })
      pushEvent(task, 'handback', {
        turn: task.summary.turnCount,
        response: 'I fixed the race in the upload test by waiting for the server to listen.',
      })
      touch(task)
    } else if (sim.kind === 'suggest' && Date.now() >= sim.until) {
      task.sim = sim.resume ?? null
      task.git.suggestion = {
        state: 'ready',
        message: `feat(${task.summary.projectName}): ${task.summary.title.toLowerCase()}`,
      }
      touch(task)
    }
  }
}

function finishSimTurn(task) {
  task.sim = null
  endTurn(task, 'ok', 'Done. Follow-up applied and the tests pass.', true)
  task.summary.status = 'done'
  task.summary.review = null
  task.summary.needsSince = now()
  touch(task)
}

function runTurn(task, message, by, steps = 6) {
  const s = task.summary
  s.status = 'running'
  s.attention = null
  delete s.needsSince
  s.review = null
  startTurn(task, message, by, true)
  task.sim = { kind: 'turn', step: 0, steps }
  touch(task)
}

const NEW_TITLES = [
  'Add a health endpoint',
  'Cache product images',
  'Write migration for audit table',
  'Improve 404 page copy',
  'Add retries to the mailer',
]
let spawned = 0
function spawnTask() {
  const t = makeTask({
    title: NEW_TITLES[spawned++ % NEW_TITLES.length],
    status: 'queued',
    project: pick(projects.slice(0, 3)).key,
    account: 'acc-personal',
    createdAt: now(),
  })
  broadcast('task.created', t.summary)
  startSoon(t)
}

/** A queued task gets an account and starts its first turn after a few seconds. */
function startSoon(task) {
  setTimeout(() => {
    const s = task.summary
    if (s.status !== 'queued') return
    if (!s.accountId) {
      s.accountId = 'acc-personal'
      s.accountLabel = 'Personal'
    }
    runTurn(task, s.title, 'lead', 14)
  }, 3000)
}

// ---------------------------------------------------------------------------------------------------------------
// Diffs (paths relative to the repo root)

function diffFor(task, path) {
  const file = task.git.files.find((f) => f.path === path)
  if (!file) return null
  const head = { path, binary: file.binary, truncated: false, hunks: [] }
  if (file.oldPath) head.oldPath = file.oldPath
  if (file.binary) return head
  const lines = []
  const ctx = (text, oldNo, newNo) => lines.push({ kind: 'context', text, oldNo, newNo })
  const add = (text, newNo) => lines.push({ kind: 'add', text, newNo })
  const del = (text, oldNo) => lines.push({ kind: 'del', text, oldNo })
  if (file.status === 'added' || file.status === 'deleted') {
    const ext = path.split('.').pop()
    const body =
      ext === 'go'
        ? [
            'package checkout',
            '',
            'import "time"',
            '',
            'type bucket struct {',
            '\ttokens float64',
            '\tlast   time.Time',
            '}',
            '',
            'func (b *bucket) take(now time.Time, rate, burst float64) bool {',
            '\tb.tokens = min(burst, b.tokens+now.Sub(b.last).Seconds()*rate)',
            '\tb.last = now',
            '\tif b.tokens < 1 {',
            '\t\treturn false',
            '\t}',
            '\tb.tokens--',
            '\treturn true',
            '}',
          ]
        : ext === 'vue'
          ? [
              '<script setup lang="ts">',
              'const route = useRoute()',
              'const { data } = await useFetch(`/api/products/${route.params.id}`)',
              '</script>',
              '',
              '<template>',
              '  <ProductCard v-if="data" :product="data" />',
              '</template>',
            ]
          : [
              'export function validateEmail(value: string): string | null {',
              '  const trimmed = value.trim()',
              "  if (!trimmed) return 'required'",
              "  if (!/^[^@\\s]+@[^@\\s]+\\.[^@\\s]+$/.test(trimmed)) return 'invalid'",
              '  return null',
              '}',
            ]
    const added = file.status === 'added'
    body.forEach((text, i) => (added ? add(text, i + 1) : del(text, i + 1)))
    const header = added ? `@@ -0,0 +1,${body.length} @@` : `@@ -1,${body.length} +0,0 @@`
    head.hunks.push({
      header,
      oldStart: added ? 0 : 1,
      oldLines: added ? 0 : body.length,
      newStart: added ? 1 : 0,
      newLines: added ? body.length : 0,
      lines,
    })
    return head
  }
  ctx('func (h *Handler) Checkout(w http.ResponseWriter, r *http.Request) {', 40, 40)
  ctx('\tctx := r.Context()', 41, 41)
  del('\torder, err := h.svc.Place(ctx, r.Body)', 42)
  add('\tif !h.limiter.Allow(clientKey(r)) {', 42)
  add('\t\thttp.Error(w, "too many requests", http.StatusTooManyRequests)', 43)
  add('\t\treturn', 44)
  add('\t}', 45)
  add('\torder, err := h.svc.Place(ctx, r.Body)', 46)
  ctx('\tif err != nil {', 43, 47)
  ctx('\t\twriteError(w, err)', 44, 48)
  head.hunks.push(
    {
      header: '@@ -40,5 +40,9 @@ func (h *Handler) Checkout',
      oldStart: 40,
      oldLines: 5,
      newStart: 40,
      newLines: 9,
      lines,
    },
    {
      header: '@@ -88,3 +92,3 @@ func NewHandler',
      oldStart: 88,
      oldLines: 3,
      newStart: 92,
      newLines: 3,
      lines: [
        { kind: 'context', text: 'func NewHandler(svc Service) *Handler {', oldNo: 88, newNo: 92 },
        { kind: 'del', text: '\treturn &Handler{svc: svc}', oldNo: 89 },
        {
          kind: 'add',
          text: '\treturn &Handler{svc: svc, limiter: newLimiter(10, 20)}',
          newNo: 93,
        },
        { kind: 'context', text: '}', oldNo: 90, newNo: 94 },
      ],
    },
  )
  return head
}

// ---------------------------------------------------------------------------------------------------------------
// SSE

const clients = new Set()
const ring = []
let frameSeq = 0

const frameId = (seq) => `${INSTANCE}.${seq}`

function broadcast(event, data) {
  frameSeq++
  const frame = { seq: frameSeq, id: frameId(frameSeq), event, data: JSON.stringify(data) }
  ring.push(frame)
  if (ring.length > RING) ring.shift()
  for (const client of clients) write(client, frame)
}

function write(res, frame) {
  let text = ''
  if (frame.id) text += `id: ${frame.id}\n`
  text += `event: ${frame.event}\ndata: ${frame.data}\n\n`
  res.write(text)
}

function openStream(req, res, url) {
  res.writeHead(200, {
    'Content-Type': 'text/event-stream',
    'Cache-Control': 'no-store',
    'X-Accel-Buffering': 'no',
    Connection: 'keep-alive',
  })
  res.write('retry: 2000\n\n')
  // Every frame has an id, like the daemon's (contract §7, docs/api.md): hello carries the current id on a fresh
  // connect and the client's id on a resume that replays; a reset carries the current id, so a reconnect after
  // it resumes from there.
  const current = frameId(frameSeq)
  const resume = req.headers['last-event-id'] ?? url.searchParams.get('lastEventId')
  let reset = ''
  let replay = []
  if (resume) {
    const match = /^([0-9a-f]{8})\.(\d+)$/.exec(resume)
    const seq = match ? Number(match[2]) : NaN
    const oldest = ring[0]?.seq ?? frameSeq + 1
    if (!match || match[1] !== INSTANCE) reset = 'restart'
    else if (seq > frameSeq || seq < oldest - 1) reset = 'too_old'
    else replay = ring.filter((frame) => frame.seq > seq)
  }
  write(res, {
    id: resume && !reset ? resume : current,
    event: 'hello',
    data: JSON.stringify({
      instance: INSTANCE,
      pid: process.pid,
      build: BUILD,
      version: '0.0.0-mock',
      serverTime: now(),
    }),
  })
  if (reset) write(res, { id: current, event: 'reset', data: JSON.stringify({ reason: reset }) })
  for (const frame of replay) write(res, frame)
  clients.add(res)
  const ping = setInterval(() => res.write(': ping\n\n'), 25_000)
  req.on('close', () => {
    clearInterval(ping)
    clients.delete(res)
  })
}

// ---------------------------------------------------------------------------------------------------------------
// HTTP

class HttpError extends Error {
  constructor(status, code, message, details) {
    super(message)
    this.status = status
    this.code = code
    this.details = details
  }
}

const notFound = (what) => new HttpError(404, 'not_found', `${what} not found`)
const badState = (message, status) => new HttpError(409, 'bad_state', message, { status })
const invalid = (message, field) => new HttpError(400, 'invalid_argument', message, { field })

function send(res, status, body) {
  if (status === 204) {
    res.writeHead(204, { 'Cache-Control': 'no-store' })
    res.end()
    return
  }
  const text = JSON.stringify(body)
  res.writeHead(status, {
    'Content-Type': 'application/json; charset=utf-8',
    'Cache-Control': 'no-store',
    'Content-Length': Buffer.byteLength(text),
  })
  res.end(text)
}

/** MOCK_DEV_ORIGIN, checked like the daemon's --dev-origin: a loopback http origin with a port, or none. */
function devOrigin(raw) {
  if (!raw) return null
  const url = new URL(raw)
  const loopback = ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname)
  if (url.protocol !== 'http:' || !url.port || !loopback || url.pathname !== '/') {
    throw new Error(`MOCK_DEV_ORIGIN ${raw}: must be http://localhost|127.0.0.1|[::1]:<port>`)
  }
  return { host: url.host.toLowerCase(), origin: url.origin.toLowerCase() }
}

/** The Host check (contract §1): 127.0.0.1, localhost or [::1] with the mock's own port, or the dev origin's. */
function hostAllowed(host) {
  const h = (host ?? '').toLowerCase()
  const own = [`127.0.0.1:${PORT}`, `localhost:${PORT}`, `[::1]:${PORT}`]
  return own.includes(h) || h === DEV_ORIGIN?.host
}

/** The same-origin check of writes: the mock's own origin, or the dev origin. */
function ownOrigin(origin) {
  const o = origin.toLowerCase().replace(/\/$/, '')
  const own = [`http://127.0.0.1:${PORT}`, `http://localhost:${PORT}`, `http://[::1]:${PORT}`]
  return own.includes(o) || o === DEV_ORIGIN?.origin
}

async function readBody(req, allowed) {
  const chunks_ = []
  let size = 0
  for await (const chunk of req) {
    size += chunk.length
    if (size > 1 << 20) throw new HttpError(413, 'too_large', 'request body is over 1 MiB')
    chunks_.push(chunk)
  }
  const text = Buffer.concat(chunks_).toString('utf8').trim()
  if (!text) return {}
  let body
  try {
    body = JSON.parse(text)
  } catch {
    throw invalid('request body is not valid JSON')
  }
  if (typeof body !== 'object' || body === null || Array.isArray(body)) {
    throw invalid('request body must be a JSON object')
  }
  for (const field of Object.keys(body)) {
    if (!allowed.includes(field)) throw invalid(`unknown field "${field}"`, field)
  }
  return body
}

function taskOr404(id) {
  const task = tasks.get(id)
  if (!task) throw notFound(`task ${id}`)
  return task
}

const ACTIVE = ['queued', 'running', 'waiting_quota']
const PENDING = [...ACTIVE, 'waiting_deps']
const TIERS = ['fast', 'standard', 'strong', 'max']
const byNewest = (a, b) =>
  Date.parse(b.summary.createdAt) - Date.parse(a.summary.createdAt) ||
  (b.summary.id < a.summary.id ? -1 : 1)

/** Tasks /state always carries: pending, with attention, done without a review. */
const always = (s) =>
  PENDING.includes(s.status) || s.attention !== null || (s.status === 'done' && !s.review)

function detail(task) {
  const p = projectOf(task.summary.projectKey)
  return {
    ...task.summary,
    prompt: task.prompt,
    repoRoot: p.repoRoot,
    error: task.error,
    turns: task.turns,
  }
}

function taskGit(task) {
  const p = projectOf(task.summary.projectKey)
  if (!p.repoRoot)
    return { repo: false, dirty: false, files: [], preexisting: [], suggestion: null }
  return {
    repo: true,
    root: p.repoRoot,
    branch: p.name === 'Seelegent' ? 'feat/web-tasks' : 'main',
    dirty: task.git.files.length > 0,
    files: task.git.files,
    preexisting: task.git.preexisting,
    suggestion: task.git.suggestion,
  }
}

function projectList() {
  return projects
    .map((p) => {
      const list = [...tasks.values()].filter((t) => t.summary.projectKey === p.key)
      const last = list.reduce((max, t) => Math.max(max, Date.parse(t.summary.updatedAt)), 0)
      return {
        ...p,
        taskCount: list.length,
        activeCount: list.filter((t) => ACTIVE.includes(t.summary.status)).length,
        lastActivityAt: new Date(last || STARTED).toISOString(),
      }
    })
    .sort((a, b) => Date.parse(b.lastActivityAt) - Date.parse(a.lastActivityAt))
}

let lowMemory = LOW_MEMORY === '1' || LOW_MEMORY === 'flip'

/** This machine's free memory as the daemon reports it (commit is known on Windows only; the mock guesses 2x). */
function memory() {
  const phys = Math.round(freemem() / 1048576)
  return { availPhysMB: phys, availCommitMB: OS === 'windows' ? phys * 2 : 0, known: true }
}

const clamp = (n, lo, hi) => Math.max(lo, Math.min(n, hi))

/** This machine's profile and the values in use for the auto settings (config.EffectiveResources, simplified). */
function machineResources() {
  const totalPhysMB = Math.round(totalmem() / 1048576)
  const totalCommitMB = OS === 'windows' ? Math.floor((totalPhysMB * 1.5) / 1024) * 1024 : 0
  const cpus = availableParallelism()
  const minFree = clamp(Math.floor(totalPhysMB / 8), 512, 2048)
  const auto = {
    minFreeMemoryMB: minFree,
    criticalFreeMemoryMB: Math.floor(minFree / 2),
    minFreeCommitMB: totalCommitMB > 0 ? clamp(Math.floor(totalCommitMB / 10), 1024, 4096) : 0,
    testParallelism: clamp(Math.floor(cpus / 4), 1, 8),
    gateConcurrency: clamp(
      Math.min(Math.floor(cpus / 8), Math.floor(totalPhysMB / 1024 / 8)),
      1,
      4,
    ),
  }
  const resources = {}
  for (const [key, value] of Object.entries(auto)) {
    const set = settings[key]
    resources[key] =
      set === undefined || set === null
        ? { value, source: 'auto' }
        : { value: set, source: 'explicit' }
  }
  // Only one of the two memory thresholds set: the other is derived from it (docs/configuration.md "Resources").
  const { minFreeMemoryMB: min, criticalFreeMemoryMB: floor } = resources
  if (min.source === 'explicit' && floor.source === 'auto') {
    const base = min.value > 0 ? min.value : auto.minFreeMemoryMB
    resources.criticalFreeMemoryMB = { value: Math.floor(base / 2), source: 'derived' }
  } else if (floor.source === 'explicit' && min.source === 'auto' && floor.value >= min.value) {
    resources.minFreeMemoryMB = { value: Math.min(floor.value * 2, 1048576), source: 'derived' }
  }
  return { resources, machine: { totalPhysMB, totalCommitMB, cpus } }
}

function health() {
  return {
    ok: true,
    version: '0.0.0-mock',
    commit: 'mock',
    build: BUILD,
    apiVersion: 1,
    pid: process.pid,
    port: PORT,
    startedAt: STARTED.toISOString(),
    homeId: 'mock0123456789ab',
    os: OS,
    memory: memory(),
    lowMemory,
    ...machineResources(),
  }
}

function flipLowMemory() {
  lowMemory = !lowMemory
  broadcast('resources.changed', { lowMemory, memory: memory() })
}

function intParam(url, name, min, max, fallback) {
  const raw = url.searchParams.get(name)
  if (raw === null || raw === '') return fallback
  const n = Number(raw)
  if (!Number.isInteger(n) || n < min || n > max) {
    throw invalid(`${name} must be an integer in ${min}..${max}`, name)
  }
  return n
}

function listTasks(url) {
  const status = url.searchParams.get('status')?.split(',').filter(Boolean) ?? []
  const project = url.searchParams.get('project') ?? ''
  const q = (url.searchParams.get('q') ?? '').toLowerCase()
  const limit = intParam(url, 'limit', 1, 200, 50)
  const list = [...tasks.values()]
    .sort(byNewest)
    .map((t) => t.summary)
    .filter(
      (s) =>
        (!status.length || status.includes(s.status)) &&
        (!project || s.projectKey === project) &&
        (!q || s.title.toLowerCase().includes(q)),
    )
  // Keyset cursor over (createdAt, id): base64 of the last row's key.
  let start = 0
  const cursor = url.searchParams.get('cursor')
  if (cursor) {
    const [at, id] = JSON.parse(Buffer.from(cursor, 'base64url').toString('utf8'))
    start = list.findIndex((s) => s.createdAt < at || (s.createdAt === at && s.id < id))
    if (start < 0) start = list.length
  }
  const items = list.slice(start, start + limit)
  const body = { items }
  const last = items.at(-1)
  if (last && start + limit < list.length) {
    body.nextCursor = Buffer.from(JSON.stringify([last.createdAt, last.id])).toString('base64url')
  }
  return body
}

async function taskWrite(req, res, task, sub) {
  const s = task.summary
  if (sub === 'followup') {
    const body = await readBody(req, [
      'message',
      'by',
      'tier',
      'model',
      'effort',
      'taskIds',
      'profile',
      'allowGit',
      'gate',
      'lane',
      'forceLane',
      'verify',
      'force',
    ])
    if (typeof body.message !== 'string' || !body.message.trim())
      throw invalid('message is required', 'message')
    if (body.by !== undefined && !['lead', 'user'].includes(body.by))
      throw invalid('by must be lead or user', 'by')
    if (body.tier !== undefined && !TIERS.includes(body.tier))
      throw invalid(`unknown tier "${body.tier}"`, 'tier')
    if (ACTIVE.includes(s.status) || s.manual) {
      throw badState(
        `task ${s.id} is ${s.manual ? 'driven by you' : s.status}; wait or cancel it first`,
        s.status,
      )
    }
    if (body.tier) s.tier = body.tier
    if (body.model) s.model = body.model
    runTurn(task, body.message, body.by ?? 'lead')
    return send(res, 200, s)
  }
  if (sub === 'answer') {
    const body = await readBody(req, ['answer'])
    if (typeof body.answer !== 'string' || !body.answer.trim())
      throw invalid('answer is required', 'answer')
    if (s.attention?.state !== 'question')
      throw badState(`task ${s.id} has no open question`, s.status)
    pushEvent(task, 'attention', { state: 'cleared' })
    endTurn(task, 'ok', 'Paused for a question.', true)
    runTurn(task, body.answer, 'user', 6)
    return send(res, 200, s)
  }
  if (sub === 'review') {
    const body = await readBody(req, ['verdict', 'notes'])
    if (body.verdict !== 'accepted' && body.verdict !== 'rejected') {
      throw invalid('verdict must be accepted or rejected', 'verdict')
    }
    if (s.status !== 'done' && s.status !== 'failed') {
      throw badState(`task ${s.id} is ${s.status}; only finished tasks can be reviewed`, s.status)
    }
    s.review = { verdict: body.verdict, at: now() }
    if (body.notes) s.review.notes = body.notes
    delete s.needsSince
    pushEvent(
      task,
      'review',
      body.notes ? { verdict: body.verdict, notes: body.notes } : { verdict: body.verdict },
    )
    touch(task)
    if (body.verdict === 'accepted') {
      for (const other of tasks.values()) {
        if (other.summary.status === 'waiting_deps' && other.summary.after.includes(s.id)) {
          pushEvent(other, 'deps', { state: 'ready', after: other.summary.after })
          other.summary.status = 'queued'
          touch(other)
          startSoon(other)
        }
      }
    }
    return send(res, 200, s)
  }
  if (sub === 'cancel') {
    const body = await readBody(req, ['waitSeconds'])
    const wait = body.waitSeconds
    if (wait !== undefined && (!Number.isInteger(wait) || wait < 0 || wait > 60)) {
      throw invalid('waitSeconds must be 0..60', 'waitSeconds')
    }
    // Like the daemon: cancelling a finished task changes nothing and answers 200 with the summary.
    if (!PENDING.includes(s.status) && s.status !== 'blocked_deps' && !s.manual) {
      return send(res, 200, s)
    }
    task.sim = null
    if (task.turns.at(-1)?.status === 'running') endTurn(task, 'cancelled', '', true)
    s.status = 'cancelled'
    s.attention = null
    s.activeStep = null
    s.manual = false
    delete s.needsSince
    pushEvent(task, 'cancel', {})
    touch(task)
    return send(res, 200, s)
  }
  if (sub === 'attention/dismiss') {
    await readBody(req, [])
    if (s.attention) {
      s.attention = null
      delete s.needsSince
      pushEvent(task, 'attention', { state: 'cleared' })
      touch(task)
    }
    return send(res, 200, s)
  }
  if (sub === 'takeover') {
    await readBody(req, [])
    if (!s.accountId || s.manual) throw badState(`task ${s.id} cannot be taken over now`, s.status)
    s.manual = true
    pushEvent(task, 'takeover', { state: 'start', accountId: s.accountId })
    task.sim = { kind: 'takeover', until: Date.now() + 15_000 }
    touch(task)
    return send(res, 200, s)
  }
  if (sub === 'git/suggest-message') {
    await readBody(req, [])
    task.git.suggestion = { state: 'running' }
    task.sim = { kind: 'suggest', until: Date.now() + 2500, resume: task.sim }
    touch(task)
    return send(res, 202, taskGit(task))
  }
  if (sub === 'git/suggest-message/dismiss') {
    await readBody(req, [])
    task.git.suggestion = null
    touch(task)
    return send(res, 200, taskGit(task))
  }
  if (sub === 'git/commit') {
    const body = await readBody(req, ['message', 'files'])
    if (typeof body.message !== 'string' || !body.message.trim())
      throw invalid('message is required', 'message')
    if (!Array.isArray(body.files) || body.files.length === 0)
      throw invalid('files must list at least one file', 'files')
    if (ACTIVE.includes(s.status))
      throw badState(`task ${s.id} is ${s.status}; commit when it has finished`, s.status)
    const unknown = body.files.find((f) => !task.git.files.some((g) => g.path === f))
    if (unknown) throw invalid(`${unknown} is not a file this task changed`, 'files')
    task.git.files = task.git.files.filter((f) => !body.files.includes(f.path))
    task.git.suggestion = null
    s.filesChanged = task.git.files.length
    touch(task)
    return send(res, 200, { sha: hex(7), summary: `${body.files.length} file(s) changed` })
  }
  throw notFound(`route POST /tasks/${s.id}/${sub}`)
}

async function route(req, res, url) {
  const method = req.method ?? 'GET'
  const path = url.pathname.replace(/^\/api\/v1/, '') || '/'
  const parts = path.split('/').filter(Boolean).map(decodeURIComponent)

  if (['POST', 'PUT', 'PATCH', 'DELETE'].includes(method)) {
    if (req.headers['x-seelegent'] !== '1') {
      throw new HttpError(
        403,
        'forbidden',
        'a request that changes something needs the header X-Seelegent: 1',
      )
    }
    const origin = req.headers.origin
    if (origin && !ownOrigin(origin)) {
      throw new HttpError(403, 'forbidden', 'requests from another origin are refused')
    }
  }

  if (method === 'GET') {
    if (path === '/health') return send(res, 200, health())
    if (path === '/events') return openStream(req, res, url)
    if (path === '/backends') return send(res, 200, backends)
    if (path === '/projects') return send(res, 200, projectList())
    if (path === '/models') {
      return send(res, 200, [
        {
          id: 'gemini-3.8-flash',
          backend: 'agy',
          label: 'Gemini 3.8 Flash',
          tiers: ['fast', 'standard'],
        },
        { id: 'gemini-3.8-pro', backend: 'agy', label: 'Gemini 3.8 Pro', tiers: ['strong'] },
      ])
    }
    if (path === '/state') {
      const all = [...tasks.values()].sort(byNewest).map((t) => t.summary)
      const pinned = all.filter(always)
      const others = all.filter((s) => !always(s)).slice(0, 100)
      return send(res, 200, {
        health: health(),
        settings,
        accounts,
        backends,
        tasks: [...pinned, ...others],
        projects: projectList(),
        lastEventId: frameId(frameSeq),
      })
    }
    if (path === '/tasks') return send(res, 200, listTasks(url))
  }

  if ((await adminRoute(req, res, method, path, parts, url)) !== false) return

  if (parts[0] === 'tasks' && parts[1]) {
    const task = taskOr404(parts[1])
    const sub = parts.slice(2).join('/')
    if (method === 'POST') return taskWrite(req, res, task, sub)
    if (method === 'GET' && sub === '') return send(res, 200, detail(task))
    if (method === 'GET' && sub === 'events') {
      const hasTail = url.searchParams.has('tail')
      if (hasTail && (url.searchParams.has('after') || url.searchParams.has('limit')))
        throw invalid('tail cannot be combined with after or limit', 'tail')
      const lastSeq = task.events.length
      if (hasTail) {
        const tail = intParam(url, 'tail', 1, 2000, 500)
        return send(res, 200, { items: task.events.slice(-tail), lastSeq })
      }
      const after = intParam(url, 'after', 0, Number.MAX_SAFE_INTEGER, 0)
      const limit = intParam(url, 'limit', 1, 2000, 500)
      return send(res, 200, { items: task.events.slice(after, after + limit), lastSeq })
    }
    if (method === 'GET' && sub === 'prompt') {
      const turn = intParam(url, 'turn', 1, 10_000, 0)
      if (!turn) throw invalid('turn is required', 'turn')
      // No attempt (or 0) means the latest attempt of the turn, as in the daemon.
      const tried = [...task.prompts.keys()]
        .filter((key) => key.startsWith(`${turn}.`))
        .map((key) => Number(key.slice(key.indexOf('.') + 1)))
      const attempt = intParam(url, 'attempt', 0, 100, 0) || Math.max(0, ...tried)
      const prompt = task.prompts.get(`${turn}.${attempt}`)
      if (prompt === undefined) throw notFound(`brief of turn ${turn} attempt ${attempt}`)
      return send(res, 200, { prompt })
    }
    if (method === 'GET' && sub === 'git') {
      if (task.statusFailures > 0) {
        task.statusFailures--
        // A git timeout as the daemon reports it (contract §1.1, §3.3): upstream with details.source git.
        throw new HttpError(502, 'upstream', 'git status: timed out', { source: 'git' })
      }
      return send(res, 200, taskGit(task))
    }
    if (method === 'GET' && sub === 'git/diff') {
      const file = url.searchParams.get('path') ?? ''
      if (!file || file.startsWith('/') || file.split('/').includes('..')) {
        throw invalid('path must be relative to the repo root and stay inside it', 'path')
      }
      const diff = diffFor(task, file)
      if (!diff) throw notFound(`file ${file}`)
      return send(res, 200, diff)
    }
  }
  throw notFound(`route ${method} ${url.pathname}`)
}

const adminRoute = createAdminRoutes({
  accounts,
  settings,
  projects,
  broadcast,
  readBody,
  send,
  HttpError,
  noHooks: process.env.MOCK_NO_HOOKS === '1',
})

const server = createServer((req, res) => {
  const url = new URL(req.url ?? '/', `http://127.0.0.1:${PORT}`)
  if (!hostAllowed(req.headers.host)) {
    // Every path (API, stream and the rest), like the daemon: the DNS-rebinding defence.
    send(res, 421, {
      error: {
        code: 'misdirected_request',
        message: 'this daemon answers only on 127.0.0.1, localhost or [::1] with its own port',
      },
    })
    return
  }
  if (!url.pathname.startsWith('/api/v1/')) {
    send(res, 404, {
      error: {
        code: 'not_found',
        message: 'the mock daemon serves /api/v1 only; open the Vite dev server',
      },
    })
    return
  }
  route(req, res, url).catch((error) => {
    if (error instanceof HttpError) {
      const body = { error: { code: error.code, message: error.message } }
      const details = Object.entries(error.details ?? {}).filter(([, v]) => v !== undefined)
      if (details.length) body.error.details = Object.fromEntries(details)
      send(res, error.status, body)
    } else {
      console.error(error)
      send(res, 500, { error: { code: 'internal', message: 'internal error' } })
    }
  })
})

seedData()
setInterval(tick, TICK_MS)
setInterval(spawnTask, 90_000)
if (LOW_MEMORY === 'flip') setInterval(flipLowMemory, 30_000)
server.listen(PORT, '127.0.0.1', () => {
  console.log(
    `mock daemon on http://127.0.0.1:${PORT}/api/v1 (instance ${INSTANCE}, ${tasks.size} tasks)`,
  )
})
