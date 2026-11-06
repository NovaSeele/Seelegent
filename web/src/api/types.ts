/**
 * HTTP API v1 data shapes, hand-written from the contract (specs/Seelegent-spec/contracts/api-v1.md) until tygo
 * generates types.gen.ts from internal/api. Names and fields follow the contract exactly so the generated file
 * can replace this one. Helper shapes the contract leaves unnamed (event payloads, SSE frames, list pages) live in
 * events.ts and the endpoint modules, not here.
 */

// §2 System

export interface Health {
  ok: true
  version: string
  commit: string
  build: string
  apiVersion: 1
  pid: number
  port: number
  startedAt: string
}

export interface BootState {
  health: Health
  settings: Settings
  accounts: Account[]
  backends: Backend[]
  tasks: TaskSummary[]
  projects: Project[]
  lastEventId: string
}

export interface Backend {
  id: 'agy' | 'claude' | 'codex'
  name: string
  enabled: boolean
  installed: boolean
  version?: string
  path?: string
  accounts: number
  active: number
  capabilities: { resume: boolean; apiKey: boolean; takeover: boolean; listModels: boolean }
}

export interface Model {
  id: string
  backend: string
  label: string
  tiers: Tier[]
}

export type Tier = 'fast' | 'standard' | 'strong' | 'max'

export interface Project {
  key: string
  cwd: string
  name: string
  repoRoot?: string
  taskCount: number
  activeCount: number
  lastActivityAt: string
}

export interface SpecView {
  dir: string
  feature: string
  tasks: { id: string; title: string; done: boolean; phase?: string; parallel: boolean }[]
}

// §3.1 Tasks

export type TaskStatus =
  | 'waiting_deps'
  | 'blocked_deps'
  | 'queued'
  | 'running'
  | 'waiting_quota'
  | 'done'
  | 'failed'
  | 'cancelled'

export type Verdict = 'accepted' | 'rejected'

export interface TaskSummary {
  id: string
  title: string
  status: TaskStatus
  /** normal, verify, consult, ... */
  kind: string
  mode: 'edit' | 'plan'
  cwd: string
  projectKey: string
  projectName: string
  backend: string
  provider?: string
  model: string
  effort?: string
  tier?: Tier
  accountId?: string
  accountLabel?: string
  member?: string
  role?: string
  profile: { id: string; name: string } | null
  session: { id: string; hostId?: string; title?: string; parentTitle?: string } | null
  turnCount: number
  tokensUsed: number
  filesChanged: number
  createdAt: string
  updatedAt: string
  lastEventAt?: string
  /** waiting_quota: when an account is expected back. */
  waitingUntil?: string
  /** Since when the task waits for the lead/user. */
  needsSince?: string
  attention: Attention | null
  review: { verdict: Verdict; notes?: string; at: string } | null
  gate: {
    state: 'running' | 'green' | 'red' | 'error' | 'skipped'
    phase?: string
    autoFixes: number
  } | null
  spec: { dir: string; taskIds: string[] } | null
  activeStep: { label: string; tool?: string; since: string } | null
  after: string[]
  afterMode: 'accepted' | 'done'
  depsReason?: string
  manual: boolean
  parentId?: string
  contestId?: string
  ticketId?: string
  meetingId?: string
  /** Highest event seq of this task (lets the UI know if its event cache is stale). */
  lastEventSeq: number
}

export interface Attention {
  state: 'blocked' | 'question' | 'stalled' | 'background_wait' | 'info'
  reason: string
  who: 'lead' | 'user'
  detail?: string
  since: string
}

export interface TaskDetail extends TaskSummary {
  /** First lead message. */
  prompt: string
  repoRoot?: string
  error?: string
  turns: Turn[]
}

export interface Turn {
  n: number
  by: 'lead' | 'user' | 'gate' | 'verifier'
  message: string
  status: 'running' | 'ok' | 'failed' | 'cancelled'
  model?: string
  requestedModel?: string
  tier?: Tier
  effort?: string
  startedAt: string
  endedAt?: string
  response?: string
  /** Relative to cwd, '/' separators. */
  files: string[]
  commands: string[]
  blockedCommands: string[]
  deniedActions: string[]
  usage: Usage | null
  attempts: Attempt[]
  git: { changed: string[]; preexisting: string[] } | null
  handback?: string
}

export interface Attempt {
  accountId: string
  accountLabel?: string
  model?: string
  startedAt: string
  endedAt?: string
  status:
    'running' | 'ok' | 'quota' | 'auth' | 'transient' | 'background_wait' | 'error' | 'cancelled'
  kind?: 'console'
  error?: string
  usage: Usage | null
}

export interface Usage {
  inputTokens: number
  outputTokens: number
  cachedTokens?: number
  totalTokens: number
  costUsd?: number
}

// §3.2 Events

// The TaskEvent union itself lives in events.ts (tygo cannot emit it); these are its envelope and data shapes.

export interface TaskEventBase {
  seq: number
  ts: string
  taskId: string
  turn?: number
  attempt?: number
}

/** The normalized worker event (internal/event.Event), stored as `type: "worker"`. */
export interface WorkerEvent {
  kind:
    | 'session'
    | 'message'
    | 'tool_call'
    | 'tool_result'
    | 'file'
    | 'usage'
    | 'report'
    | 'error'
    | 'log'
    | 'background_wait'
  step?: number
  text?: string
  session?: WorkerSession
  tool?: WorkerTool
  file?: WorkerFile
  usage?: Usage
  report?: WorkerReport
  error?: WorkerError
}

export interface WorkerSession {
  conversationId: string
  model?: string
}

export interface WorkerTool {
  id?: string
  name: string
  state: 'calling' | 'done' | 'failed'
  /** The tool's parameters as JSON (secrets masked). */
  input?: unknown
  command?: string
  output?: string
  durationMs?: number
  blocked?: boolean
}

export interface WorkerFile {
  path: string
  op: 'read' | 'edit'
  tool?: string
}

export interface WorkerReport {
  status: 'success' | 'error'
  response?: string
  error?: string
  conversationId?: string
  usage?: Usage
  durationMs?: number
  deniedActions?: string[]
  synthesized?: boolean
}

export interface WorkerError {
  class: 'quota_exhausted' | 'auth_required' | 'transient' | 'fatal'
  message: string
}

export interface LeadMessageData {
  message: string
  by: 'lead' | 'user' | 'gate' | 'verifier'
  model?: string
  effort?: string
  tier?: Tier
}

export interface RouteData {
  accountId: string
  /** Never an email. */
  accountLabel: string
  model: string
  reason: string
  continuing: boolean
  remaining?: number
}

/** The brief itself is read with GET /tasks/{id}/prompt. */
export interface WorkerPromptData {
  length: number
  /** First 500 characters. */
  preview: string
}

export interface AttemptEndData {
  status: Attempt['status']
  error?: string
}

export interface TurnEndData {
  status: Turn['status']
  response?: string
  files: string[]
  usage?: Usage
}

/** quota_exhausted and auth_failed. */
export interface AccountFailureData {
  accountId: string
  group?: string
  until?: string
  error: string
}

export interface WaitingQuotaData {
  until?: string
}

export interface RetryData {
  delayMs: number
  error: string
}

export interface HandoffData {
  from: string
  to: string
  reason: string
}

/** reattach and resumed. */
export interface ReattachData {
  accountId: string
  offset?: number
}

export type AttentionEventData = Attention | { state: 'cleared' }

export interface DepsData {
  state: 'waiting' | 'ready' | 'blocked'
  after?: string[]
  reason?: string
  forced?: boolean
}

export interface TakeoverData {
  state: 'start' | 'end'
  accountId: string
  durationMs?: number
  exitCode?: number
}

export interface HandbackData {
  turn: number
  response: string
}

export interface ReviewData {
  verdict: Verdict
  notes?: string
}

export type CancelData = Record<string, never>

export interface WorkAtRiskData {
  files: string[]
  snapshot?: string
}

export interface ErrorData {
  message: string
}

export interface DelegateRequest {
  prompt: string
  title?: string
  cwd: string
  mode?: 'edit' | 'plan'
  tier?: Tier
  model?: string
  effort?: string
  backend?: string
  provider?: string
  member?: string
  role?: string
  profile?: string
  spec?: { dir: string; taskIds?: string[] }
  after?: string[]
  afterMode?: 'accepted' | 'done'
  allowGit?: boolean
  gate?: boolean
  lane?: string[]
  verify?: boolean
  session?: { id: string; hostId?: string; title?: string; parentTitle?: string }
}

export interface FollowupRequest {
  message: string
  by?: 'lead' | 'user'
  tier?: Tier
  model?: string
  effort?: string
  taskIds?: string[]
  profile?: string
  allowGit?: boolean
  gate?: boolean
  lane?: string[]
  forceLane?: boolean
  verify?: boolean
  /** Start a waiting/blocked task anyway. */
  force?: boolean
}

export interface WaitRequest {
  ids: string[]
  /** 0..3000 */
  timeoutSeconds: number
  any?: boolean
  /** Default true. */
  wakeOnBlocked?: boolean
  /** Include TaskReport[]. */
  reports?: boolean
}

export interface WaitResult {
  done: boolean
  woke: 'done' | 'blocked' | 'timeout' | 'cancelled'
  tasks: TaskSummary[]
  reports?: TaskReport[]
}

// §3.3 Git and files

export interface TaskGit {
  repo: boolean
  root?: string
  branch?: string
  dirty: boolean
  files: GitFile[]
  preexisting: string[]
  suggestion: { state: 'idle' | 'running' | 'ready' | 'failed'; message?: string } | null
}

export interface GitFile {
  path: string
  status: 'added' | 'modified' | 'deleted' | 'renamed' | 'untracked'
  oldPath?: string
  additions: number
  deletions: number
  turn?: number
  binary: boolean
}

export type RepoStatus =
  | { repo: false }
  | {
      repo: true
      root: string
      branch: string
      detached: boolean
      dirty: boolean
      files: GitFile[]
    }

export interface FileDiff {
  path: string
  oldPath?: string
  binary: boolean
  truncated: boolean
  hunks: {
    header: string
    oldStart: number
    oldLines: number
    newStart: number
    newLines: number
    lines: { kind: 'context' | 'add' | 'del'; text: string; oldNo?: number; newNo?: number }[]
  }[]
}

// §3.4 TaskReport: the lead-facing slim report (snake_case, MCP shape). The dashboard does not read it; only the
// fields every report has are typed, the optional ones stay open.

export interface TaskReport {
  task_id: string
  status: string
  turn: number
  worker: string
  model: string
  tokens_used: number
  response: string
  files_changed: string[]
  git_changes: unknown
  commands_run: string[]
  dashboard: string
  [field: string]: unknown
}

// §4 Accounts, providers, quota, usage

export interface Account {
  id: string
  backend: 'agy' | 'claude' | 'codex'
  kind: 'isolated' | 'system' | 'api-key'
  label: string
  enabled: boolean
  priority: number
  provider?: string
  keyHint?: string
  state: 'ready' | 'busy' | 'resting' | 'exhausted' | 'auth_required' | 'signing_in' | 'disabled'
  /** Shown in the local UI only; never logged. */
  email?: string
  busy: number
  maxConcurrent: number
  quota: QuotaWindow[]
  restingUntil?: string
  lastRefreshAt?: string
  lastError?: string
  models: string[]
  createdAt: string
  updatedAt: string
}

export interface QuotaWindow {
  /** Model group, e.g. "gemini-pro". */
  group: string
  label: string
  /** 0..1, null = unknown. */
  remaining: number | null
  resetsAt?: string
  exhaustedUntil?: string
  source: 'cli' | 'api' | 'observed'
}

export interface LoginSession {
  accountId: string
  state: 'starting' | 'open_url' | 'waiting_code' | 'done' | 'failed'
  url?: string
  userCode?: string
  message?: string
}

export interface Provider {
  id: string
  name: string
  backend: 'claude' | 'codex'
  baseUrl: string
  enabled: boolean
  models: string[]
  accounts: number
}

export interface UsageSummary {
  days: number
  since: string
  totals: { tasks: number; turns: number; tokens: number }
  perAccount: {
    accountId: string
    label: string
    backend: string
    turns: number
    tokens: number
    remaining: number | null
  }[]
  perModel: { model: string; turns: number; tokens: number }[]
}

export interface UsageHistory {
  bucket: 'hour' | 'day'
  since: string
  until: string
  firstAt?: string
  rows: { at: string; accountId: string; group: string; remaining: number | null; tokens: number }[]
}

// §5 Settings: config.json of internal/config.Settings in camelCase. The nested backend shapes are prefixed
// with Settings here because the Go names (Backend, Backends) would clash with the API's Backend.

export interface TierModels {
  fast: string
  standard: string
  strong: string
  max: string
}

export interface SettingsBackend {
  enabled: boolean
  allowedModels: string
  tiers: TierModels
}

export interface SettingsCodexBackend extends SettingsBackend {
  sandbox: string
}

export interface Settings {
  port: number
  defaultTier: string
  backends: { agy: SettingsBackend; codex: SettingsCodexBackend; claude: SettingsBackend }
  maxConcurrentPerAccount: number
  quotaRefreshMinutes: number
  minRemaining: number
  passiveCooldownMinutes: number
  usageHistory: boolean
  taskTimeoutMinutes: number
  linkIntoProfiles: string[]
  stallMinutes: number
  stallToolMinutes: number
  loopRepeat: number
  attentionDecayHours: number
  gates: boolean
  gateAutoChecks: boolean
  gateTimeoutSec: number
  gateMaxAutoFixes: number
  gateSettleMinutes: number
  gateSettlePollMs: number
  gateOutputTailBytes: number
  hygiene: boolean
  lanes: string
  laneHoldHours: number
  verifier: boolean
  verifierTier: string
  gitGuard: boolean
  codeGraph: boolean
  snapshotRetentionDays: number
  contentionWindowHours: number
  githubPollMinutes: number
  automationsEnabled: boolean
}
