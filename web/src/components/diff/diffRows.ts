/**
 * Rows of a parsed diff (contract §3.3: the daemon parses, the UI never reads unified diff text). A hunk header
 * row precedes each hunk's lines; past `limit` lines the rest is counted, not built, so a huge diff stays cheap
 * until someone asks for all of it.
 */
import type { FileDiff } from '@/api/types'

export type DiffLineKind = 'context' | 'add' | 'del'

export type DiffRow =
  | { key: string; kind: 'hunk'; text: string }
  | { key: string; kind: DiffLineKind; text: string; oldNo?: number; newNo?: number }

export interface DiffRows {
  rows: DiffRow[]
  /** Lines left out because of the limit. */
  hidden: number
  additions: number
  deletions: number
}

export function diffRows(diff: FileDiff, limit = Infinity): DiffRows {
  const rows: DiffRow[] = []
  let lines = 0
  let hidden = 0
  let additions = 0
  let deletions = 0
  diff.hunks.forEach((hunk, h) => {
    for (const line of hunk.lines) {
      if (line.kind === 'add') additions++
      else if (line.kind === 'del') deletions++
    }
    if (lines >= limit) {
      hidden += hunk.lines.length
      return
    }
    rows.push({ key: `h${h}`, kind: 'hunk', text: hunk.header })
    hunk.lines.forEach((line, i) => {
      if (lines >= limit) {
        hidden++
        return
      }
      lines++
      rows.push({
        key: `h${h}.${i}`,
        kind: line.kind,
        text: line.text,
        oldNo: line.oldNo,
        newNo: line.newNo,
      })
    })
  })
  return { rows, hidden, additions, deletions }
}

/** Total diff lines, hunk headers excluded. */
export const diffLineCount = (diff: FileDiff) =>
  diff.hunks.reduce((sum, hunk) => sum + hunk.lines.length, 0)
