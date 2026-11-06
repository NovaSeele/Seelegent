import { existsSync, readdirSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { readFile, readFiles, webRoot } from '@/test/files'

import {
  FILE_ICON_SET_IDS,
  FILE_ICON_SETS,
  fileIcon,
  fileIconAssets,
  fileIconKind,
  fileIconSpecialName,
  normalizeFileIconSet,
} from './fileicons'

describe('fileIconKind', () => {
  it.each([
    ['web/src/App.vue', 'vue'],
    ['cmd/seelegent/main.go', 'go'],
    ['internal/store/store_test.go', 'test'],
    ['src/lib/keys.spec.ts', 'test'],
    ['go.mod', 'cfg'],
    ['tsconfig.json', 'cfg'],
    ['package.json', 'cfg'],
    ['package-lock.json', 'json'],
    ['.gitignore', 'cfg'],
    ['docs/ui-layout.md', 'md'],
    ['web/src/', 'folder'],
    ['C:\\work\\main.ts', 'ts'],
    ['"quoted name.css"', 'css'],
    ['Makefile', 'file'],
  ])('%s is %s', (path, kind) => {
    expect(fileIconKind(path)).toBe(kind)
  })

  it('finds special names, folders included', () => {
    expect(fileIconSpecialName('web/README.md')).toBe('readme.md')
    expect(fileIconSpecialName('a/b.test.ts')).toBe('*.test.ts')
    expect(fileIconSpecialName('web/src/')).toBe('src/')
    expect(fileIconSpecialName('src')).toBe('')
  })
})

describe('fileIcon', () => {
  it('draws Seelegent glyphs for the built-in sets and nothing when off', () => {
    expect(fileIcon('main.ts', 'seelegent')).toMatchObject({ kind: 'ts', src: null, mono: false })
    expect(fileIcon('main.ts', 'seelegent-mono')).toMatchObject({ src: null, mono: true })
    expect(fileIcon('main.ts', 'off')).toBeNull()
  })

  it('uses the set SVG, its special names and its light/dark or flavour folders', () => {
    expect(fileIcon('main.ts', 'seti')?.src).toBe('/fileicons/seti/typescript.svg')
    expect(fileIcon('package.json', 'seti')?.src).toBe('/fileicons/seti/npm.svg')
    expect(fileIcon('data.json', 'jetbrains', 'dark')?.src).toBe(
      '/fileicons/jetbrains/json_dark.svg',
    )
    expect(fileIcon('data.json', 'jetbrains', 'light')?.src).toBe('/fileicons/jetbrains/json.svg')
    expect(fileIcon('main.ts', 'catppuccin', 'light')?.src).toBe(
      '/fileicons/catppuccin/latte/typescript.svg',
    )
    expect(fileIcon('main.ts', 'catppuccin', 'dark')?.src).toBe(
      '/fileicons/catppuccin/mocha/typescript.svg',
    )
    expect(fileIcon('web/src/', 'material')?.src).toBe('/fileicons/material/folder-src.svg')
  })

  it('falls back to the set file icon, then to the Seelegent glyph', () => {
    expect(fileIcon('App.vue', 'catppuccin')?.src).toBe('/fileicons/catppuccin/mocha/_file.svg')
    expect(fileIcon('main.ts', 'jetbrains')?.src).toBe('/fileicons/jetbrains/anyType_dark.svg')
    expect(fileIcon('App.vue', 'material')).toMatchObject({ kind: 'vue', src: null })
    expect(fileIcon('docs/', 'material')).toMatchObject({ kind: 'folder', src: null })
  })

  it('reads values saved by the old dashboard', () => {
    expect(normalizeFileIconSet('color')).toBe('seelegent')
    expect(normalizeFileIconSet('mono')).toBe('seelegent-mono')
    expect(normalizeFileIconSet('jetbrains')).toBe('jetbrains')
    expect(normalizeFileIconSet('nope')).toBeNull()
    expect(normalizeFileIconSet(3)).toBeNull()
  })
})

describe('third-party icon assets (public/fileicons)', () => {
  const svgSets = FILE_ICON_SET_IDS.filter((id) => FILE_ICON_SETS[id].kind === 'svg')

  it.each(svgSets)('%s ships every file it maps, with its licence and source notes', (id) => {
    const missing = fileIconAssets(id).filter(
      (file) => !existsSync(join(webRoot, 'public/fileicons', file)),
    )
    expect(missing).toEqual([])
    const notes = readdirSync(join(webRoot, 'public/fileicons', id))
    expect(notes.some((name) => name.startsWith('LICENSE'))).toBe(true)
    expect(notes).toContain('SOURCE.md')
  })

  it('keeps JetBrains pinned to the Apache-2.0 commit, with its NOTICE', () => {
    expect(readFile('public/fileicons/jetbrains/SOURCE.md')).toContain('ad5656c6')
    expect(readFile('public/fileicons/jetbrains/LICENSE.txt')).toContain('Apache License')
    expect(existsSync(join(webRoot, 'public/fileicons/jetbrains/NOTICE.txt'))).toBe(true)
  })

  it('contains only drawing markup: no scripts, handlers or outside references', () => {
    const svgs = readFiles('public/fileicons', ['.svg'])
    expect(Object.keys(svgs).length).toBeGreaterThan(60)
    const unsafe = Object.entries(svgs)
      .filter(([, svg]) =>
        /<script|<foreignObject|\son\w+\s*=|(?:xlink:)?href\s*=\s*["'](?!#)|url\(\s*["']?(?!#)/i.test(
          svg,
        ),
      )
      .map(([path]) => path)
    expect(unsafe).toEqual([])
  })

  it('is listed in THIRD_PARTY_NOTICES.md at the repository root', () => {
    const notices = readFile('../THIRD_PARTY_NOTICES.md')
    for (const id of svgSets) expect(notices).toContain(`web/public/fileicons/${id}/`)
  })
})
