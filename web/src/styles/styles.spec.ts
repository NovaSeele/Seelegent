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

/** Every non-theme file with its CSS. */
const styled = Object.entries(sources)
  .filter(([path]) => !path.startsWith(THEMES_DIR))
  .map(([path, source]) => [path, styleText(path, source)] as const)

describe('style gates', () => {
  // File-type glyph colours are fixed per type (like VS Code) and provider hues per backend, not themed, so each
  // lives in a table of its own.
  const COLOUR_TABLES = ['src/styles/fileicons.css', 'src/styles/providers.css']

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

// A frame that clips or scrolls (overflow auto/scroll/hidden) without a position of its own does not contain its
// absolute descendants, and every .sr-only label is absolute. Those labels escape the frame and stretch the scroll
// height of the nearest positioned ancestor instead. It came back twice: first the Tree tab scrolled the whole task
// pane away (fixed in the process views only), then the /tasks sidebar's 47 ProviderMark labels gave .workspace a
// 4260px scroll height in a 1062px pane, so scrolling the list to its end chained into an empty workspace. Hence the
// gate covers every auto/scroll frame in src. overflow:hidden is not gated (text ellipsis spans need no position):
// clipping frames that hold .sr-only or absolute children are positioned by hand, and .list__rows is locked below as
// the example. (.sr-only itself stays as it is: the settings pickers' hidden radios rely on it.)
describe('scroll frames', () => {
  interface Rule {
    path: string
    selectors: string[]
    body: string
    /** Inside @media, @container, @supports or another at-rule: it does not apply everywhere. */
    conditional: boolean
  }

  /** The innermost blocks of `css`, each with its selector list and whether an at-rule encloses it. */
  function parseRules(path: string, css: string): Rule[] {
    const text = css.replace(/\/\*[\s\S]*?\*\//g, '')
    const out: Rule[] = []
    const open: { prelude: string; start: number; leaf: boolean }[] = []
    let mark = 0
    for (let i = 0; i < text.length; i++) {
      if (text[i] === '{') {
        const parent = open.at(-1)
        if (parent) parent.leaf = false
        open.push({ prelude: text.slice(mark, i).trim(), start: i + 1, leaf: true })
        mark = i + 1
      } else if (text[i] === '}') {
        const block = open.pop()
        if (block?.leaf) {
          out.push({
            path,
            selectors: block.prelude
              .split(',')
              .map((s) => s.trim().replace(/\s+/g, ' '))
              .filter(Boolean),
            body: text.slice(block.start, i),
            conditional: open.some((b) => b.prelude.startsWith('@')),
          })
        }
        mark = i + 1
      }
    }
    return out
  }

  const rules: Rule[] = styled.flatMap(([path, css]) => parseRules(path, css))

  const scrolls = (body: string) =>
    [...body.matchAll(/(?:^|[;{\s])overflow(?:-x|-y)?\s*:\s*([^;]*)/g)].some((m) =>
      /\b(?:auto|scroll)\b/.test(m[1] ?? ''),
    )
  const positioned = (body: string) =>
    /(?:^|[;{\s])position\s*:\s*(?:relative|absolute|sticky|fixed)\b/.test(body)
  // Only rules outside at-rules count: a position set under @media (e.g. .git__list's sticky from 900px) leaves the
  // frame unpositioned at every other size.
  const ruleFor = (path: string, selector: string) =>
    rules.filter(
      (rule) => rule.path === path && !rule.conditional && rule.selectors.includes(selector),
    )

  it('positions every scroll frame', () => {
    const frames = rules.filter((rule) => scrolls(rule.body))
    // Guards the parser: a regression that finds no frames would pass vacuously.
    expect(frames.length).toBeGreaterThan(30)
    const missing = frames
      .filter((rule) => !positioned(rule.body))
      .flatMap((rule) =>
        rule.selectors
          .filter(
            (selector) => !ruleFor(rule.path, selector).some((other) => positioned(other.body)),
          )
          .map((selector) => `${rule.path}: ${selector}`),
      )
    expect(missing).toEqual([])
  })

  it('keeps the task sidebar scroll from chaining into the workspace', () => {
    const body = ruleFor('src/components/layout/SplitLayout.vue', '.split__sidebar-body')
      .map((rule) => rule.body)
      .join('\n')
    expect(body).toMatch(/overscroll-behavior:\s*contain/)
    expect(positioned(body)).toBe(true)
  })

  it('positions the account list clipping frame', () => {
    const body = ruleFor('src/features/accounts/AccountList.vue', '.list__rows')
      .map((rule) => rule.body)
      .join('\n')
    expect(body).toMatch(/overflow:\s*hidden/)
    expect(positioned(body)).toBe(true)
  })
})
