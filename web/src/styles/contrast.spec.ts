/**
 * Contrast gate: in every theme, the main text / background token pairs meet WCAG 2.x contrast. The table is
 * generated here from the palette files and the derived layer of tokens.css, so a new theme or a changed token is
 * checked without updating a fixture.
 *
 *   4.5:1  all text: primary, secondary and muted on every surface, text on the accent, links (accent), status
 *          words on their tinted pill (cancelled too), terminal text and its dim text
 *   3:1    only UI parts whose colour carries meaning: the focus ring and the outline of a control that has no
 *          fill of its own (checkbox, --border-control)
 *
 * --border-subtle / --border-strong are separators between areas that already differ in background or spacing;
 * they carry no meaning on their own and are not checked.
 */
import { describe, expect, it } from 'vitest'

import { THEMES } from '@/config/themes'
import { readFile } from '@/test/files'

type Rgba = readonly [number, number, number, number]

interface Rule {
  fg: string
  on: readonly string[]
  min: number
  /** A translucent background is first laid over this opaque one (tinted pills sit on a surface). */
  base?: string
}

const SURFACES = ['--bg-app', '--bg-surface', '--bg-surface-2', '--bg-elevated'] as const

const TONES = [
  ['--status-running-fg', '--status-running-bg'],
  ['--attention-fg', '--attention-bg'],
  ['--status-accepted-fg', '--status-accepted-bg'],
  ['--status-danger-fg', '--status-danger-bg'],
  ['--neutral-fg', '--neutral-bg'],
] as const

const RULES: readonly Rule[] = [
  { fg: '--text-primary', on: [...SURFACES, '--bg-hover'], min: 4.5 },
  { fg: '--text-secondary', on: SURFACES, min: 4.5 },
  { fg: '--text-muted', on: SURFACES, min: 4.5 },
  { fg: '--text-on-accent', on: ['--accent'], min: 4.5 },
  { fg: '--link', on: ['--bg-app', '--bg-surface'], min: 4.5 },
  { fg: '--border-focus', on: ['--bg-app', '--bg-surface'], min: 3 },
  { fg: '--border-control', on: ['--bg-surface'], min: 3 },
  ...TONES.map(([fg, bg]) => ({ fg, on: [bg], min: 4.5, base: '--bg-surface' })),
  {
    fg: '--status-cancelled-fg',
    on: ['--status-cancelled-bg'],
    min: 4.5,
    base: '--bg-surface',
  },
  { fg: '--term-fg', on: ['--term-bg'], min: 4.5 },
  { fg: '--term-dim', on: ['--term-bg'], min: 4.5 },
]

/** The custom properties declared in a CSS block (last declaration wins). */
function declarations(css: string): Map<string, string> {
  const out = new Map<string, string>()
  const body = css.replace(/\/\*[\s\S]*?\*\//g, '')
  for (const [, name, value] of body.matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)) {
    out.set(name as string, (value as string).trim())
  }
  return out
}

/** The derived colour layer of tokens.css (the block shared by :root and [data-theme-preview]). */
function derivedLayer(): Map<string, string> {
  const tokens = readFile('src/styles/tokens.css')
  const block = /:root,\s*\[data-theme-preview\]\s*\{([\s\S]*?)\n\}/.exec(tokens)
  if (!block?.[1]) throw new Error('tokens.css: derived colour layer not found')
  return declarations(block[1])
}

function parseColor(value: string): Rgba | null {
  const hex = /^#([0-9a-f]{3}|[0-9a-f]{6}|[0-9a-f]{8})$/i.exec(value)
  if (hex?.[1]) {
    let h = hex[1]
    if (h.length === 3) h = [...h].map((c) => c + c).join('')
    const n = (i: number) => parseInt(h.slice(i, i + 2), 16)
    return [n(0), n(2), n(4), h.length === 8 ? n(6) / 255 : 1]
  }
  const fn = /^rgba?\(([^)]+)\)$/i.exec(value)
  if (fn?.[1]) {
    const [r = 0, g = 0, b = 0, a = 1] = fn[1]
      .split(/[\s,/]+/)
      .filter(Boolean)
      .map(Number)
    return [r, g, b, a]
  }
  return null
}

function resolver(theme: Map<string, string>, derived: Map<string, string>) {
  return function resolve(name: string, depth = 0): Rgba {
    const raw = theme.get(name) ?? derived.get(name)
    if (raw === undefined) throw new Error(`${name} is not defined`)
    const ref = /^var\((--[\w-]+)\)$/.exec(raw)
    if (ref?.[1]) {
      if (depth > 8) throw new Error(`${name}: var() loop`)
      return resolve(ref[1], depth + 1)
    }
    const color = parseColor(raw)
    if (!color) throw new Error(`${name}: cannot read "${raw}"`)
    return color
  }
}

const over = (top: Rgba, bottom: Rgba): Rgba => [
  top[0] * top[3] + bottom[0] * (1 - top[3]),
  top[1] * top[3] + bottom[1] * (1 - top[3]),
  top[2] * top[3] + bottom[2] * (1 - top[3]),
  1,
]

function luminance([r, g, b]: Rgba): number {
  const lin = (v: number) => {
    const c = v / 255
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  }
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b)
}

function contrast(a: Rgba, b: Rgba): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number]
  return (hi + 0.05) / (lo + 0.05)
}

const derived = derivedLayer()

function violations(id: string): string[] {
  const resolve = resolver(declarations(readFile(`src/styles/themes/${id}.css`)), derived)
  const out: string[] = []
  for (const rule of RULES) {
    for (const bgName of rule.on) {
      let bg = resolve(bgName)
      if (bg[3] < 1) bg = over(bg, resolve(rule.base ?? '--bg-app'))
      const ratio = contrast(over(resolve(rule.fg), bg), bg)
      if (ratio < rule.min) out.push(`${rule.fg} on ${bgName}: ${ratio.toFixed(2)} < ${rule.min}`)
    }
  }
  return out
}

describe('theme contrast (WCAG)', () => {
  it('computes the WCAG ratio', () => {
    expect(contrast([0, 0, 0, 1], [255, 255, 255, 1])).toBeCloseTo(21, 5)
    expect(contrast([118, 118, 118, 1], [255, 255, 255, 1])).toBeCloseTo(4.54, 2)
  })

  it.each(THEMES.map((theme) => theme.id))('%s meets the contrast table', (id) => {
    expect(violations(id)).toEqual([])
  })
})
