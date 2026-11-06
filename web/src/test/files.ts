/** Test helpers that read source files from disk (CSS is not importable as text under Vitest). */
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative, sep } from 'node:path'

// Vitest runs with web/ as its root and working directory.
export const webRoot = process.cwd()
export const srcRoot = join(webRoot, 'src')

/** All files under `dir` (relative to web/) whose name ends with one of `exts`, keyed by a web/-relative path. */
export function readFiles(dir: string, exts: readonly string[]): Record<string, string> {
  const out: Record<string, string> = {}
  const walk = (abs: string) => {
    for (const name of readdirSync(abs)) {
      const path = join(abs, name)
      if (statSync(path).isDirectory()) walk(path)
      else if (exts.some((ext) => name.endsWith(ext))) {
        out[relative(webRoot, path).split(sep).join('/')] = readFileSync(path, 'utf8')
      }
    }
  }
  walk(join(webRoot, dir))
  return out
}

export function readFile(path: string): string {
  return readFileSync(join(webRoot, path), 'utf8')
}
