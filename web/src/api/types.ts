/**
 * HTTP API v1 data shapes, hand-written from the contract (specs/Seelegent-spec/contracts/api-v1.md) until tygo
 * generates types.gen.ts from internal/api. Names and fields follow the contract exactly so the generated file
 * can replace this one. Helper shapes the contract leaves unnamed (event payloads, SSE frames, list pages) live in
 * events.ts and the endpoint modules, not here.
 *
 * Type names equal the Go struct names tygo will emit. tygo writes one file per Go package, but this file is one
 * namespace, so names that two packages share, or that shadow a TypeScript global, keep a package prefix:
 *   internal/quota.Snapshot -> QuotaSnapshot     internal/usage.Snapshot -> UsageSnapshot
 *   internal/quota.Window   -> QuotaWindow       internal/usage.Window   -> UsageWindow
 *   internal/quota.Error    -> QuotaError        (Error is the JavaScript global)
 * Every other name is the Go name as it is (quota.Group, usage.Summary, Totals, DayTotal, TurnUsage, TaskUsage,
 * ProjectUsage, ModelUsage, AccountUsage, Day, History).
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

export type AccountStatus =
  'ok' | 'error' | 'new' | 'needs_login' | 'no_key' | 'no_plan' | 'twin' | 'disabled'

export interface Account {
  id: string
  backend: 'agy' | 'claude' | 'codex'
  kind: 'isolated' | 'system' | 'api-key'
  label: string
  enabled: boolean
  priority: number
  provider?: string
  keyHint?: string
  /**
   * The pool's status, computed on read. UI wording: ok → Ready, new → Checking quota, error → Ready (last read
   * failed, old numbers), needs_login → Sign-in required, no_key → No API key, no_plan → Plan cannot use this
   * backend, twin → Same sign-in as twinOf, disabled → Off.
   */
  status: AccountStatus
  twinOf?: string
  /** Running turns. */
  busy: number
  maxConcurrent: number
  /** A cooldown, or a window at/below minRemaining. */
  resting: boolean
  restingUntil?: string
  signingIn: boolean
  /** Shown in the local UI only; never logged. */
  email?: string
  quota: QuotaSnapshot
  models: string[]
  createdAt: string
  updatedAt: string
}

/** internal/quota.Snapshot. */
export interface QuotaSnapshot {
  /** Read from the CLI/service, or only observed from runs (API-key accounts: groups []). */
  source: 'active' | 'passive'
  fetchedAt?: string
  plan?: string
  noPlan?: boolean
  noPlanReason?: string
  exhaustedUntil?: string
  exhaustedReason?: string
  /** A group "<group> <family>" is a sub-limit of <group>. */
  groups: Group[]
  /** Absent when the last read succeeded. */
  error?: QuotaError
}

/** internal/quota.Error. */
export interface QuotaError {
  kind: 'needs_login' | 'token_expired' | 'format' | 'unavailable' | 'failed'
  message: string
  at: string
}

/** internal/quota.Group. */
export interface Group {
  name: string
  windows: QuotaWindow[]
}

export interface QuotaWindow {
  /** "5h", "weekly", "30d", "<n>m", "window". */
  window: string
  label?: string
  minutes?: number
  /** 0..1 */
  remaining: number
  resetAt?: string
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

// internal/usage: Summary and History are the source of truth for these shapes.

export interface Totals {
  tokens: number
  costUsd: number
  /** Some attempt reported a cost. */
  hasCost: boolean
  tasks: number
  turns: number
  attempts: number
}

export interface DayTotal {
  date: string
  tokens: number
  costUsd: number
  tasks: number
}

export interface TurnUsage {
  n: number
  tokens: number
  costUsd: number
  durationMs: number
  attempts: number
  at: string
  accountId: string
  account: string
  model: string
}

export interface TaskUsage {
  id: string
  title: string
  status: string
  backend: string
  tokens: number
  costUsd: number
  durationMs: number
  lastAt: string
  accountId: string
  account: string
  turns: number
  items: TurnUsage[]
  more: number
}

export interface ProjectUsage {
  key: string
  name: string
  tokens: number
  costUsd: number
  durationMs: number
  tasks: number
  turns: number
  items: TaskUsage[]
  more: number
}

export interface ModelUsage {
  model: string
  tokens: number
  costUsd: number
  attempts: number
  durationMs: number
}

export interface AccountUsage {
  id: string
  /** Never an email (replaced by the id). */
  label: string
  backend: string
  tokens: number
  costUsd: number
  attempts: number
  durationMs: number
  models: ModelUsage[]
  more: number
}

export interface Summary {
  days: number
  since: string
  until: string
  previousSince: string
  /** The oldest attempt kept; absent when there is none. */
  dataSince?: string
  totals: Totals
  previous: Totals
  daily: DayTotal[]
  projects: ProjectUsage[]
  moreProjects: number
  accounts: AccountUsage[]
  moreAccounts: number
}

/** One quota window in a usage snapshot (internal/usage.Window). */
export interface UsageWindow {
  group: string
  window: string
  label?: string
  remaining: number
  resetAt?: string
  /** The lowest remaining of the day (daily rollup only). */
  minRemaining?: number
}

/** One account at one hour. */
export interface UsageSnapshot {
  at: string
  accountId: string
  backend: string
  label: string
  enabled: boolean
  status: string
  plan?: string
  quota: UsageWindow[]
  /** Usage on the snapshot's local day so far. */
  tokensToday: number
  costToday: number
}

/** One account on one local day: the lowest remaining per window, the largest running counters. */
export interface Day {
  date: string
  accountId: string
  backend: string
  label: string
  enabled: boolean
  status: string
  plan?: string
  samples: number
  firstAt: string
  lastAt: string
  quota: UsageWindow[]
  tokensToday: number
  costToday: number
}

export interface History {
  bucket: 'hour' | 'day'
  since: string
  until: string
  /** The oldest snapshot kept; absent when there is none. */
  firstAt?: string
  /** bucket hour. */
  rows?: UsageSnapshot[]
  /** bucket day. */
  days?: Day[]
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

/**
 * A JSON merge patch of Settings (RFC 7396) for PATCH /settings: nested objects merge, arrays are replaced whole,
 * and `null` resets that key to its default.
 */
export type SettingsPatch = MergePatch<Settings>

type MergePatch<T> = {
  [K in keyof T]?:
    (T[K] extends readonly unknown[] ? T[K] : T[K] extends object ? MergePatch<T[K]> : T[K]) | null
}

// §4 Providers list

/** A preset is a provider template: everything but the account count. */
export type ProviderPreset = Omit<Provider, 'accounts'>

export interface ProviderList {
  items: Provider[]
  presets: ProviderPreset[]
}

// §5 Memory and roles (internal/api: Memory, Role, RoleProject, RoleList, Skill)

export interface Memory {
  cwd: string
  text: string
  updatedAt?: string
}

export type Effort = 'low' | 'medium' | 'high' | 'xhigh' | 'max'
export type RoleScope = 'global' | 'project'

export interface Role {
  /** ^[a-z0-9][a-z0-9-]{0,39}$ */
  id: string
  name: string
  description: string
  rules: string
  outputStyle: string
  skills: string[]
  defaultTier?: Tier
  defaultEffort?: Effort
  preferredAccounts: string[]
  /** Where the role is stored; ignored in POST /roles bodies (scope and cwd travel next to the role). */
  scope: RoleScope
  /** The project folder of a project role. */
  cwd?: string
  /** A project role that hides a global role with the same id. */
  overridesGlobal: boolean
  createdAt?: string
  updatedAt?: string
}

export interface RoleProject {
  key: string
  cwd: string
  name: string
  count: number
}

export interface RoleList {
  items: Role[]
  projects: RoleProject[]
}

/** root: project (.agents/skills at or above the folder), user (~/.agents/skills), gemini or claude. */
export interface Skill {
  name: string
  description: string
  root: string
}

// §5 Hooks. The contract pins the shape later (M14: "follows the old one in camelCase"); this is the old
// hooks.json entry plus the stable id the contract requires.

export interface HookConfig {
  /** Stable id; '' for a hook not saved yet. */
  id: string
  /** Event names, e.g. task.done, account.exhausted. */
  on: string[]
  /** A shell command. */
  run: string
  /** Only tasks in this folder fire the hook. */
  project?: string
  timeoutSeconds: number
  enabled: boolean
}

export interface HookRun {
  hookId: string
  event: string
  at: string
  exitCode?: number
  durationMs: number
  timedOut: boolean
}

export interface Hook extends HookConfig {
  lastRun: HookRun | null
}
