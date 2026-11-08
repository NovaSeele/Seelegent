/**
 * What the post-turn safety checks found for a task, gathered from its record for the safety panel: work at
 * risk per turn (newest first), the files it shares with other tasks, the hygiene and scope findings of its
 * latest finished turn, and the commands the git guard refused.
 */
import type { FileOverlap, Finding, RiskFile, TaskDetail, Turn } from '@/api/types'

export interface RiskGroup {
  turn: number
  files: RiskFile[]
  /** Files not restored yet. */
  open: number
  /**
   * The turn's changes outside the edit tools can be shown and concern its risk: the turn has snapshots and a file
   * at risk was changed outside the edit tools (a large removal by an edit tool is not in that diff).
   */
  outsideDiff: boolean
}

export interface BlockedCommand {
  turn: number
  command: string
}

export interface Safety {
  risk: RiskGroup[]
  overlaps: FileOverlap[]
  /** Hygiene then scope findings of the latest finished turn (older turns' findings are stale). */
  findings: Finding[]
  findingsTurn: number | null
  blocked: BlockedCommand[]
  /** Turns whose start or end snapshot hit its limits (files past them were not checked), newest first. */
  truncated: number[]
}

/** The limits of one snapshot (internal/snapshot: MaxFiles, MaxTotalBytes in MiB), for the truncation notice. */
export const SNAPSHOT_LIMITS = { files: 3000, mib: 256 } as const

/** How many things of each kind the panel lists; `risk` counts files not restored yet, `restored` the others. */
export interface SafetyParts {
  risk: number
  overlaps: number
  findings: number
  blocked: number
  restored: number
}

const finished = (turn: Turn) => turn.status !== 'running'

export function safetyOf(detail: TaskDetail | null): Safety {
  const turns = [...(detail?.turns ?? [])].sort((a, b) => b.n - a.n)
  const risk = turns
    .filter((turn) => turn.workAtRisk?.length)
    .map((turn) => ({
      turn: turn.n,
      files: turn.workAtRisk,
      open: turn.workAtRisk.filter((f) => !f.restored).length,
      outsideDiff: turn.outsideDiff && turn.workAtRisk.some((f) => f.why === 'outside_tools'),
    }))
  const latest = turns.find(finished)
  const findings = latest ? [...(latest.hygiene ?? []), ...(latest.scope ?? [])] : []
  const blocked = turns.flatMap((turn) =>
    (turn.blockedCommands ?? []).map((command) => ({ turn: turn.n, command })),
  )
  return {
    risk,
    overlaps: detail?.fileOverlaps ?? [],
    findings,
    findingsTurn: findings.length && latest ? latest.n : null,
    blocked,
    truncated: turns.filter((turn) => turn.snapshotTruncated).map((turn) => turn.n),
  }
}

export function safetyParts(s: Safety): SafetyParts {
  const files = s.risk.reduce((sum, g) => sum + g.files.length, 0)
  const open = s.risk.reduce((sum, g) => sum + g.open, 0)
  return {
    risk: open,
    overlaps: s.overlaps.length,
    findings: s.findings.length,
    blocked: s.blocked.length,
    restored: files - open,
  }
}

/** How many open things the panel lists (restored work is done, not counted). */
export function safetyCount(s: Safety): number {
  const p = safetyParts(s)
  return p.risk + p.overlaps + p.findings + p.blocked
}

/** Something needs a look now: work not restored, a file another running task also changes, a red finding. */
export function safetyUrgent(s: Safety): boolean {
  return (
    s.risk.some((g) => g.open > 0) ||
    s.overlaps.some((o) => o.severity === 'active') ||
    s.findings.some((f) => f.severity === 'red')
  )
}

/** Every file at risk of every turn has been restored. */
export const safetyRestored = (s: Safety) => s.risk.length > 0 && s.risk.every((g) => !g.open)

/** The turns whose changes outside the edit tools can be read, newest first. */
export const outsideTurns = (detail: TaskDetail | null | undefined): number[] =>
  (detail?.turns ?? [])
    .filter((turn) => turn.outsideDiff)
    .map((turn) => turn.n)
    .sort((a, b) => b - a)

/** The restored files of each turn, for the outside diff's "Restored" marks. */
export function restoredByTurn(detail: TaskDetail | null | undefined): Record<number, string[]> {
  const out: Record<number, string[]> = {}
  for (const turn of detail?.turns ?? []) {
    const paths = (turn.workAtRisk ?? []).filter((f) => f.restored).map((f) => f.path)
    if (paths.length) out[turn.n] = paths
  }
  return out
}

/** The i18n key of a finding's kind (eol_changed → eolChanged). */
export const findingKey = (kind: Finding['kind']) =>
  `task.safety.finding.${kind.replace(/_([a-z])/g, (_, ch: string) => ch.toUpperCase())}`
