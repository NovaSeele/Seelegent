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
  }
}

/** How many things the panel lists; 0 hides it. */
export function safetyCount(s: Safety): number {
  return (
    s.risk.reduce((sum, g) => sum + g.files.length, 0) +
    s.overlaps.length +
    s.findings.length +
    s.blocked.length
  )
}

/** Something needs a look now: work not restored, a file another running task also changes, a red finding. */
export function safetyUrgent(s: Safety): boolean {
  return (
    s.risk.some((g) => g.open > 0) ||
    s.overlaps.some((o) => o.severity === 'active') ||
    s.findings.some((f) => f.severity === 'red')
  )
}

/** The i18n key of a finding's kind (eol_changed → eolChanged). */
export const findingKey = (kind: Finding['kind']) =>
  `task.safety.finding.${kind.replace(/_([a-z])/g, (_, ch: string) => ch.toUpperCase())}`
