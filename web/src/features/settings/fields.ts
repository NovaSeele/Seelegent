/**
 * The daemon's settings (internal/config.Settings, contract §5) as rows of the settings dialog: which section
 * shows each one, its control and its unit. Ranges are the daemon's (internal/config/validate.go); the daemon
 * checks them again and its field errors show next to the field. Labels and descriptions are
 * settings.items.<item>.{label,desc,keywords}.
 */
import type { Settings } from '@/api/types'

export type Unit = 'min' | 'h' | 's' | 'ms' | 'days' | 'bytes' | 'times' | 'pct' | 'mb'

interface Base {
  /** settings.items.<item> */
  item: string
  /** Dotted path into Settings, e.g. "stallMinutes". */
  path: keyof Settings & string
  /** The only system the setting works on (Health.os, Go's GOOS); elsewhere the row says it has no effect. */
  os?: 'windows'
  /** The free memory reading (Health.memory) the row shows next to its threshold: physical or commit. */
  now?: 'phys' | 'commit'
}

export type FieldDef =
  /** `zero`: what 0 means (the hint says so). */
  | (Base & { kind: 'number'; unit?: Unit; min: number; max: number; zero?: 'off' | 'unlimited' })
  /** A 0..1 fraction shown as a percentage. */
  | (Base & { kind: 'percent'; min: number; max: number })
  | (Base & { kind: 'bool' })
  /** One of `options`; labels are settings.options.<optionsKey>.<value>. */
  | (Base & { kind: 'select'; options: readonly string[]; optionsKey: string })
  /** A list of strings, one per line. */
  | (Base & { kind: 'lines' })
  | (Base & { kind: 'readonly' })

const TIERS = ['fast', 'standard', 'strong', 'max'] as const
const PRIORITIES = ['low', 'normal'] as const
/** config.MemoryMBMax: the largest memory threshold or cap, in MB. */
const MEMORY_MB_MAX = 1048576

export const FIELDS = {
  workers: [
    { item: 'concurrency', path: 'maxConcurrentPerAccount', kind: 'number', min: 1, max: 64 },
    {
      item: 'defaultTier',
      path: 'defaultTier',
      kind: 'select',
      options: TIERS,
      optionsKey: 'tier',
    },
    {
      item: 'taskTimeout',
      path: 'taskTimeoutMinutes',
      kind: 'number',
      unit: 'min',
      min: 1,
      max: 10080,
    },
    { item: 'stall', path: 'stallMinutes', kind: 'number', unit: 'min', min: 1, max: 10080 },
    {
      item: 'stallTool',
      path: 'stallToolMinutes',
      kind: 'number',
      unit: 'min',
      min: 1,
      max: 10080,
    },
    { item: 'loopRepeat', path: 'loopRepeat', kind: 'number', unit: 'times', min: 2, max: 100 },
    {
      item: 'attentionDecay',
      path: 'attentionDecayHours',
      kind: 'number',
      unit: 'h',
      min: 1,
      max: 8760,
    },
    { item: 'linkIntoProfiles', path: 'linkIntoProfiles', kind: 'lines' },
  ],
  quota: [
    { item: 'quotaReserve', path: 'minRemaining', kind: 'percent', min: 0, max: 99 },
    {
      item: 'quotaRefresh',
      path: 'quotaRefreshMinutes',
      kind: 'number',
      unit: 'min',
      min: 1,
      max: 10080,
    },
    {
      item: 'passiveCooldown',
      path: 'passiveCooldownMinutes',
      kind: 'number',
      unit: 'min',
      min: 1,
      max: 10080,
    },
    { item: 'usageHistory', path: 'usageHistory', kind: 'bool' },
  ],
  gates: [
    { item: 'gates', path: 'gates', kind: 'bool' },
    { item: 'gateAutoChecks', path: 'gateAutoChecks', kind: 'bool' },
    { item: 'gateTimeout', path: 'gateTimeoutSec', kind: 'number', unit: 's', min: 1, max: 86400 },
    { item: 'gateMaxAutoFixes', path: 'gateMaxAutoFixes', kind: 'number', min: 0, max: 10 },
    {
      item: 'gateSettle',
      path: 'gateSettleMinutes',
      kind: 'number',
      unit: 'min',
      min: 0,
      max: 10080,
    },
    {
      item: 'gateSettlePoll',
      path: 'gateSettlePollMs',
      kind: 'number',
      unit: 'ms',
      min: 100,
      max: 3600000,
    },
    {
      item: 'gateOutputTail',
      path: 'gateOutputTailBytes',
      kind: 'number',
      unit: 'bytes',
      min: 1,
      max: 1048576,
    },
    { item: 'verifier', path: 'verifier', kind: 'bool' },
    {
      item: 'verifierTier',
      path: 'verifierTier',
      kind: 'select',
      options: TIERS,
      optionsKey: 'tier',
    },
  ],
  resources: [
    {
      item: 'workerPriority',
      path: 'workerPriority',
      kind: 'select',
      options: PRIORITIES,
      optionsKey: 'priority',
    },
    {
      item: 'gatePriority',
      path: 'gatePriority',
      kind: 'select',
      options: PRIORITIES,
      optionsKey: 'priority',
    },
    {
      item: 'maxWorkers',
      path: 'maxConcurrentWorkers',
      kind: 'number',
      min: 0,
      max: 256,
      zero: 'unlimited',
    },
    {
      item: 'gateConcurrency',
      path: 'gateConcurrency',
      kind: 'number',
      min: 0,
      max: 64,
      zero: 'unlimited',
    },
    {
      item: 'minFreeMemory',
      path: 'minFreeMemoryMB',
      kind: 'number',
      unit: 'mb',
      min: 0,
      max: MEMORY_MB_MAX,
      zero: 'off',
      now: 'phys',
    },
    {
      item: 'minFreeCommit',
      path: 'minFreeCommitMB',
      kind: 'number',
      unit: 'mb',
      min: 0,
      max: MEMORY_MB_MAX,
      zero: 'off',
      os: 'windows',
      now: 'commit',
    },
    {
      item: 'criticalFreeMemory',
      path: 'criticalFreeMemoryMB',
      kind: 'number',
      unit: 'mb',
      min: 0,
      max: MEMORY_MB_MAX,
      zero: 'off',
      now: 'phys',
    },
    {
      item: 'workerMemoryLimit',
      path: 'workerMemoryLimitMB',
      kind: 'number',
      unit: 'mb',
      min: 0,
      max: MEMORY_MB_MAX,
      zero: 'off',
      os: 'windows',
    },
    {
      item: 'workerCpuRate',
      path: 'workerCPURatePercent',
      kind: 'number',
      unit: 'pct',
      min: 0,
      max: 100,
      zero: 'off',
      os: 'windows',
    },
    {
      item: 'testParallelism',
      path: 'testParallelism',
      kind: 'number',
      min: 0,
      max: 64,
      zero: 'off',
    },
    {
      item: 'daemonMemoryLimit',
      path: 'daemonMemoryLimitMB',
      kind: 'number',
      unit: 'mb',
      min: 0,
      max: 65536,
      zero: 'off',
    },
  ],
  workspace: [
    { item: 'hygiene', path: 'hygiene', kind: 'bool' },
    { item: 'gitGuard', path: 'gitGuard', kind: 'bool' },
    {
      item: 'lanes',
      path: 'lanes',
      kind: 'select',
      options: ['off', 'warn', 'enforce'],
      optionsKey: 'lanes',
    },
    { item: 'laneHold', path: 'laneHoldHours', kind: 'number', unit: 'h', min: 0, max: 8760 },
    {
      item: 'contentionWindow',
      path: 'contentionWindowHours',
      kind: 'number',
      unit: 'h',
      min: 1,
      max: 8760,
    },
    {
      item: 'snapshotRetention',
      path: 'snapshotRetentionDays',
      kind: 'number',
      unit: 'days',
      min: 1,
      max: 3650,
    },
    { item: 'codeGraph', path: 'codeGraph', kind: 'bool' },
  ],
  system: [
    { item: 'port', path: 'port', kind: 'readonly' },
    {
      item: 'githubPoll',
      path: 'githubPollMinutes',
      kind: 'number',
      unit: 'min',
      min: 0,
      max: 10080,
    },
    { item: 'githubHosts', path: 'githubEnterpriseHosts', kind: 'lines' },
    { item: 'automations', path: 'automationsEnabled', kind: 'bool' },
  ],
} as const satisfies Record<string, readonly FieldDef[]>

export type FieldSection = keyof typeof FIELDS

export const isFieldSection = (id: string): id is FieldSection => Object.hasOwn(FIELDS, id)

/**
 * Settings checked against each other (internal/config/validate.go): an error about one is shown on all of them,
 * since changing either one fixes it.
 */
export const LINKED_FIELDS: Readonly<Record<string, readonly string[]>> = {
  criticalFreeMemoryMB: ['minFreeMemoryMB'],
  minFreeMemoryMB: ['criticalFreeMemoryMB'],
}

/**
 * The cross-field rule the dashboard can check before sending: criticalFreeMemoryMB must be below minFreeMemoryMB
 * while both are set (an auto one, null or absent, is derived by the daemon to stay in order) and on. Returns the
 * paths that clash with `path` set to `value`, or [] when it is fine.
 */
export function crossFieldClash(settings: Settings | null, path: string, value: number): string[] {
  if (!settings || (path !== 'criticalFreeMemoryMB' && path !== 'minFreeMemoryMB')) return []
  const critical = path === 'criticalFreeMemoryMB' ? value : settings.criticalFreeMemoryMB
  const min = path === 'minFreeMemoryMB' ? value : settings.minFreeMemoryMB
  return critical != null && min != null && critical > 0 && min > 0 && critical >= min
    ? ['criticalFreeMemoryMB', 'minFreeMemoryMB']
    : []
}

/** The value at a dotted path of the settings. */
export function valueAt(settings: Settings | null, path: string): unknown {
  let node: unknown = settings
  for (const key of path.split('.')) {
    if (typeof node !== 'object' || node === null) return undefined
    node = (node as Record<string, unknown>)[key]
  }
  return node
}

/** A merge patch that sets `path` to `value` (null resets it to the default). */
export function patchAt(path: string, value: unknown): Record<string, unknown> {
  const keys = path.split('.')
  let patch: unknown = value
  for (const key of keys.reverse()) patch = { [key]: patch }
  return patch as Record<string, unknown>
}
