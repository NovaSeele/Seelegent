/**
 * The Files view's model: every file the worker read or edited, with how often and in which turns, grouped by
 * folder. Paths are shown relative to the task folder (display only); `gitPath` is the same file relative to the
 * repository root, the form the Git view and the daemon use, and is set only for files inside the repository.
 */
import { dirName, joinPath, looksWindows, relativeTo } from '@/lib/paths'

import type { Step } from './process'

export interface FileRow {
  /** As the worker reported it. */
  path: string
  /** Relative to the task folder when inside it. */
  rel: string
  gitPath?: string
  outside: boolean
  reads: number
  edits: number
  turns: number[]
  last: number
}

export interface FolderGroup {
  /** '' for the task folder itself. */
  folder: string
  files: FileRow[]
}

export interface FilesModel {
  folders: FolderGroup[]
  read: number
  edited: number
  outside: number
}

const isAbsolute = (path: string) => /^([a-z]:[\\/]|[\\/])/i.test(path)

export function buildFiles(
  steps: readonly Step[],
  cwd: string,
  repoRoot: string | undefined,
): FilesModel {
  const rows = new Map<string, FileRow>()
  for (const step of steps) {
    if (!step.path || !step.op) continue
    const absolute = isAbsolute(step.path)
    const rel = absolute ? relativeTo(step.path, cwd) : step.path.replace(/\\/g, '/')
    // One row per file: Windows paths fold case (C:\A.ts and c:/a.ts are the same file), others never do.
    const key = looksWindows(absolute ? step.path : cwd) ? rel.toLowerCase() : rel
    let row = rows.get(key)
    if (!row) {
      const outside = absolute ? rel === step.path.replace(/\\/g, '/') : rel.startsWith('../')
      const full = absolute ? joinPath(step.path, '.') : joinPath(cwd, rel)
      const inRepo = repoRoot ? relativeTo(full, repoRoot) : undefined
      row = {
        path: step.path,
        rel,
        gitPath: inRepo && inRepo !== full ? inRepo : undefined,
        outside,
        reads: 0,
        edits: 0,
        turns: [],
        last: 0,
      }
      rows.set(key, row)
    }
    if (step.op === 'edit') row.edits++
    else row.reads++
    if (!row.turns.includes(step.turn)) row.turns.push(step.turn)
    row.last = Math.max(row.last, step.start)
  }

  const groups = new Map<string, FileRow[]>()
  for (const row of rows.values()) {
    const folder = dirName(row.rel)
    const list = groups.get(folder) ?? []
    list.push(row)
    groups.set(folder, list)
  }
  const folders = [...groups.entries()]
    .map(([folder, files]) => ({
      folder,
      files: files.sort((a, b) => a.rel.localeCompare(b.rel)),
    }))
    .sort((a, b) => {
      const ao = a.files[0]?.outside ? 1 : 0
      const bo = b.files[0]?.outside ? 1 : 0
      return ao - bo || a.folder.localeCompare(b.folder)
    })
  const all = [...rows.values()]
  return {
    folders,
    read: all.filter((row) => row.reads > 0).length,
    edited: all.filter((row) => row.edits > 0).length,
    outside: all.filter((row) => row.outside).length,
  }
}
