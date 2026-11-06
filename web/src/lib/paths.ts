/**
 * Display helpers for file paths from workers and the daemon. Workers report absolute paths with either
 * separator; the UI shows them relative to the task's folder when they are inside it. Display only: git reads and
 * commits use the daemon's repo-relative paths as they are.
 */

const slashes = (path: string) => path.replace(/\\/g, '/')

/** A Windows path (drive letter or UNC, either separator): those compare without case, others as they are. */
export const looksWindows = (path: string) => /^[a-z]:[\\/]/i.test(path) || /^[\\/]{2}/.test(path)

/** `path` relative to `base` when it lies inside it, otherwise the path with '/' separators. */
export function relativeTo(path: string, base: string | undefined): string {
  const p = slashes(path)
  if (!base) return p
  const b = slashes(base).replace(/\/+$/, '')
  const fold = looksWindows(b) ? (s: string) => s.toLowerCase() : (s: string) => s
  if (fold(p) === fold(b)) return '.'
  if (fold(p).startsWith(`${fold(b)}/`)) return p.slice(b.length + 1)
  return p
}

/** `rel` resolved against `base` ('.' and '..' segments applied), with '/' separators. */
export function joinPath(base: string, rel: string): string {
  const parts = `${slashes(base).replace(/\/+$/, '')}/${slashes(rel)}`.split('/')
  const out: string[] = []
  parts.forEach((part, i) => {
    if (part === '.' || (part === '' && i > 0)) return
    if (part === '..' && out.length > 1) out.pop()
    else if (part !== '..') out.push(part)
  })
  return out.join('/') || '/'
}

/** The last segment of a path. */
export function baseName(path: string): string {
  const p = slashes(path).replace(/\/+$/, '')
  return p.slice(p.lastIndexOf('/') + 1) || p
}

/** The folder part of a path ('' for a bare name). */
export function dirName(path: string): string {
  const p = slashes(path).replace(/\/+$/, '')
  const at = p.lastIndexOf('/')
  return at < 0 ? '' : p.slice(0, at)
}
