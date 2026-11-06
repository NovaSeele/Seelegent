/**
 * Pure helpers of the Git view. Every path is relative to the repository root (contract §3.3), so a monorepo
 * package's file reads `packages/web/pages/[id].vue`; a rename is one entry carrying `oldPath`.
 */
import type { GitFile } from '@/api/types'

export const STATUS_LETTER: Readonly<Record<GitFile['status'], string>> = {
  added: 'A',
  modified: 'M',
  deleted: 'D',
  renamed: 'R',
  untracked: 'U',
}

export function sortFiles(files: readonly GitFile[]): GitFile[] {
  return [...files].sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0))
}

export function totals(files: readonly GitFile[]): { additions: number; deletions: number } {
  let additions = 0
  let deletions = 0
  for (const file of files) {
    additions += file.additions
    deletions += file.deletions
  }
  return { additions, deletions }
}

/** Splits a repo path into its folder (with the trailing '/', or '') and file name. */
export function splitPath(path: string): { dir: string; name: string } {
  const cut = path.lastIndexOf('/')
  return cut < 0
    ? { dir: '', name: path }
    : { dir: path.slice(0, cut + 1), name: path.slice(cut + 1) }
}

/** Changes whenever the file's diff would: a new status, other counts or another rename source. */
export const fileSignature = (file: GitFile) =>
  `${file.status}:${file.additions}:${file.deletions}:${file.oldPath ?? ''}`

/** The path `delta` steps from `current`, clamped to the list; the first file when nothing is selected. */
export function stepFile(
  files: readonly GitFile[],
  current: string | null,
  delta: number,
): string | null {
  if (!files.length) return null
  const index = files.findIndex((file) => file.path === current)
  if (index < 0) return files[0]?.path ?? null
  return files[Math.min(files.length - 1, Math.max(0, index + delta))]?.path ?? null
}

/** The selected paths that are still among the task's files, in list order: what a commit may send. */
export const committable = (files: readonly GitFile[], selected: ReadonlySet<string>) =>
  files.filter((file) => selected.has(file.path)).map((file) => file.path)

/** Commit message drafts by task id, newest kept: a draft survives switching views or reloading the page. */
export type Drafts = Record<string, { message: string; at: number }>

export const MAX_DRAFTS = 20

const isDraft = (entry: unknown) =>
  typeof entry === 'object' &&
  entry !== null &&
  typeof (entry as { message?: unknown }).message === 'string' &&
  typeof (entry as { at?: unknown }).at === 'number'

export const isDrafts = (value: unknown): value is Drafts =>
  typeof value === 'object' &&
  value !== null &&
  !Array.isArray(value) &&
  Object.values(value).every(isDraft)

/** Sets (or, for blank text, removes) one draft and drops the oldest past MAX_DRAFTS. */
export function putDraft(drafts: Drafts, taskId: string, message: string, now: number): Drafts {
  const next: Drafts = { ...drafts }
  delete next[taskId]
  if (message.trim()) next[taskId] = { message, at: now }
  const ids = Object.keys(next).sort((a, b) => (next[b]?.at ?? 0) - (next[a]?.at ?? 0))
  for (const id of ids.slice(MAX_DRAFTS)) delete next[id]
  return next
}
