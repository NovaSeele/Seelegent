/**
 * Style gates: the theme registry matches the palette files, every palette sets the full base token list,
 * colours appear only in theme files, and media queries use only the shared breakpoints.
 */
import { describe, expect, it } from 'vitest'

import { BREAKPOINTS, HEIGHT_BREAKPOINTS } from '@/config/breakpoints'
import { THEMES } from '@/config/themes'
import { readFiles } from '@/test/files'

const THEMES_DIR = 'src/styles/themes/'
const themeFiles = readFiles(THEMES_DIR, ['.css'])
const sources = readFiles('src', ['.vue', '.css'])

const idOf = (path: string) => path.replace(THEMES_DIR, '').replace('.css', '')

/** Base palette every theme must define; everything else derives from it in tokens.css. */
const REQUIRED = [
  'color-scheme',
  '--bg-app',
  '--bg-surface',
  '--bg-surface-2',
  '--bg-elevated',
  '--bg-hover',
  '--bg-active',
  '--bg-code',
  '--bg-backdrop',
  '--border-subtle',
  '--border-strong',
  '--border-focus',
  '--text-primary',
  '--text-secondary',
  '--text-muted',
  '--text-on-accent',
  '--accent',
  '--accent-hover',
  '--accent-soft',
  '--accent-border',
  '--status-queued-fg',
  '--status-queued-bg',
  '--status-queued-border',
  '--status-running-fg',
  '--status-running-bg',
  '--status-running-border',
  '--attention-fg',
  '--attention-bg',
  '--attention-border',
  '--status-accepted-fg',
  '--status-accepted-bg',
  '--status-accepted-border',
  '--status-danger-fg',
  '--status-danger-bg',
  '--status-danger-border',
  '--shadow-sm',
  '--shadow-md',
  '--shadow-lg',
  '--term-bg',
  '--term-fg',
  '--term-dim',
  '--term-prompt-bg',
  '--term-prompt-fg',
  '--term-accent',
  '--term-ok',
  '--term-err',
  '--term-warn',
  '--term-tool',
  '--term-path',
  '--term-diff-add-bg',
  '--term-diff-add-fg',
  '--term-diff-del-bg',
  '--term-diff-del-fg',
  '--term-border',
  '--font-mono-theme',
]

/** The CSS a file contributes: whole .css files, only the <style> blocks of .vue files. */
function styleText(path: string, source: string): string {
  if (path.endsWith('.css')) return source
  return [...source.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map((m) => m[1]).join('\n')
}

describe('themes', () => {
  it('has one palette file per registered theme', () => {
    const files = Object.keys(themeFiles).map(idOf).sort()
    expect(files).toEqual(THEMES.map((theme) => theme.id).sort())
    expect(files).toHaveLength(26)
  })

  it.each(THEMES.map((theme) => [theme.id, theme.scheme] as const))(
    '%s defines the full palette',
    (id, scheme) => {
      const css = themeFiles[`${THEMES_DIR}${id}.css`] ?? ''
      expect(css).toContain(`:root[data-theme='${id}']`)
      expect(css).toContain(`[data-theme-preview='${id}']`)
      for (const token of REQUIRED) expect(css, `${id} ${token}`).toMatch(new RegExp(`${token}:`))
      expect(css).toContain(`color-scheme: ${scheme};`)
    },
  )

  it('ships both light and dark themes', () => {
    expect(THEMES.filter((theme) => theme.scheme === 'light')).toHaveLength(9)
    expect(THEMES.filter((theme) => theme.scheme === 'dark')).toHaveLength(17)
  })
})

describe('style gates', () => {
  const styled = Object.entries(sources)
    .filter(([path]) => !path.startsWith(THEMES_DIR))
    .map(([path, source]) => [path, styleText(path, source)] as const)

  // File-type glyph colours are fixed per type (like VS Code), not themed, so they live in one table of their own.
  const COLOUR_TABLES = ['src/styles/fileicons.css']

  it('keeps colour literals inside theme files', () => {
    const literal =
      /#[0-9a-f]{3,8}\b|\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch|color)\(|:\s*(?:white|black)\b/i
    const offenders = styled
      .filter(([path, css]) => !COLOUR_TABLES.includes(path) && literal.test(css))
      .map(([path]) => path)
    expect(offenders).toEqual([])
  })

  it('uses only the shared breakpoints, in range syntax', () => {
    const widths = new Set<number>([BREAKPOINTS.compact, BREAKPOINTS.rail])
    const heights = new Set<number>([HEIGHT_BREAKPOINTS.short])
    const problems: string[] = []
    for (const [path, css] of styled) {
      for (const [query] of css.matchAll(/@media[^{]*/g)) {
        if (/(min|max)-(width|height)/.test(query))
          problems.push(`${path}: ${query.trim()} (use range syntax)`)
        for (const [, feature, px] of query.matchAll(
          /(width|height)\s*[<>]=?\s*(\d+(?:\.\d+)?)px/g,
        )) {
          const allowed = feature === 'width' ? widths : heights
          if (!allowed.has(Number(px))) problems.push(`${path}: ${query.trim()}`)
        }
        const all = [...query.matchAll(/\d+(?:\.\d+)?px/g)].length
        const ranged = [...query.matchAll(/(?:width|height)\s*[<>]=?\s*\d+(?:\.\d+)?px/g)].length
        if (all !== ranged) problems.push(`${path}: ${query.trim()} (px outside a range)`)
      }
    }
    expect(problems).toEqual([])
  })

  it('never sets a font size below 11px', () => {
    const small = styled.filter(([, css]) => /font-size:\s*(?:[0-9]|10)(?:\.\d+)?px/.test(css))
    expect(small.map(([path]) => path)).toEqual([])
  })
})

// Design review (Tree tab): a scroll frame without a position does not contain its absolute descendants (every
// .sr-only label is absolute), so they stretch the scroll height of the pane around it and the whole task pane
// scrolls away. The process views' scroll frames are positioned.
describe('task view scroll frames', () => {
  it('position every scroll frame of the process views', () => {
    const missing: string[] = []
    for (const [path, source] of Object.entries(sources)) {
      if (!path.startsWith('src/features/tasks/views/process/') || !path.endsWith('.vue')) continue
      const css = [...source.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map((m) => m[1]).join('\n')
      for (const m of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
        const body = m[2] ?? ''
        const scrolls = /overflow(-x|-y)?:\s*(auto|scroll)/.test(body)
        if (scrolls && !/position:\s*(relative|absolute|sticky|fixed)/.test(body)) {
          missing.push(`${path}: ${(m[1] ?? '').trim()}`)
        }
      }
    }
    expect(missing).toEqual([])
  })
})
