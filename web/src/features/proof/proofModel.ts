/** Pure helpers of the Proof page: repositories to pick from, status tones and count segments, run merging. */
import type { Project, ProofCounts, ProofIndex, ProofRun, ProofStatus } from '@/api/types'
import type { Tone } from '@/components/ui/tone'
import { baseName, pathKey } from '@/lib/paths'

export interface RepoOption {
  /** The folder the proof routes are asked about (the repository root when known). */
  cwd: string
  name: string
}

/**
 * One option per repository: projects that share a repository root appear once, named after the root folder. A
 * project whose tasks never recorded a root (plan tasks only) is offered by its folder; the daemon finds the root.
 */
export function repoOptions(projects: readonly Project[]): RepoOption[] {
  const seen = new Map<string, RepoOption>()
  for (const p of projects) {
    const root = p.repoRoot || p.cwd
    const key = pathKey(root)
    if (seen.has(key)) continue
    seen.set(key, { cwd: root, name: p.repoRoot ? baseName(root) || root : p.name })
  }
  return [...seen.values()].sort((a, b) => a.name.localeCompare(b.name))
}

export const STATUS_ORDER: readonly ProofStatus[] = [
  'proven',
  'unproven',
  'in_progress',
  'unmapped',
  'failing',
  'regressed',
]

export function statusTone(status: ProofStatus): Tone {
  switch (status) {
    case 'proven':
      return 'success'
    case 'failing':
    case 'regressed':
      return 'danger'
    case 'unproven':
      return 'attention'
    case 'in_progress':
      return 'running'
    default:
      return 'neutral'
  }
}

export function runTone(state: ProofRun['state']): Tone {
  switch (state) {
    case 'green':
      return 'success'
    case 'red':
    case 'error':
      return 'danger'
    case 'running':
      return 'running'
    default:
      return 'muted'
  }
}

/** The count of each status, in display order, without the empty ones. */
export function countParts(c: ProofCounts): { status: ProofStatus; n: number }[] {
  return STATUS_ORDER.map((status) => ({ status, n: c[status] })).filter((p) => p.n > 0)
}

export function total(c: ProofCounts): number {
  return STATUS_ORDER.reduce((sum, s) => sum + c[s], 0)
}

/** The index with a run's newer state merged in (a run of another repository is ignored; newest first). */
export function withRun(index: ProofIndex, run: ProofRun): ProofIndex {
  if (run.repoRoot && pathKey(run.repoRoot) !== pathKey(index.repoRoot)) return index
  const runs = index.runs.filter((r) => r.id !== run.id)
  runs.push(run)
  runs.sort((a, b) => b.startedAt.localeCompare(a.startedAt))
  return { ...index, runs }
}
