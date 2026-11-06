/**
 * File-type icons (Files, Git and process views, terminal), ported from the old dashboard's ui/js/fileicons.js.
 * Pure: no DOM, no store. `fileIcon(path, set, scheme)` says what to draw; <FileIcon> draws it.
 *
 * Sets, in picker order (Settings › Appearance › File icons):
 *   seelegent        Seelegent's own glyphs in per-type colours (styles/fileicons.css)
 *   seelegent-mono   the same glyphs in the secondary text colour
 *   seti, material,  third-party SVGs under public/fileicons/<set>/ (each folder keeps its LICENSE and SOURCE.md;
 *   jetbrains,       THIRD_PARTY_NOTICES.md lists them). Drawn as <img>, which never runs scripts.
 *   catppuccin
 *   off              no icon and no gap
 *
 * A path is sorted into a kind (folder, test, config, then by extension, else file). A set may also have icons
 * for special names (basename, case-insensitive; "*.x" = suffix; "name/" = folder), which beat the kind. An SVG
 * set that lacks a kind falls back to its own `file` icon, and when it has none (or for folders), to the
 * Seelegent glyph.
 */
import type { ThemeScheme } from '@/config/themes'

export const FILE_ICON_KINDS = [
  'js',
  'ts',
  'vue',
  'go',
  'css',
  'json',
  'rs',
  'html',
  'md',
  'test',
  'img',
  'sh',
  'cfg',
  'folder',
  'file',
] as const

export type FileIconKind = (typeof FILE_ICON_KINDS)[number]

/** A Seelegent glyph: short text in a 16×16 box, or a stroke path in a 24-unit box. */
export type Glyph = { text: string; size: number } | { path: string }

export const GLYPHS: Readonly<Record<FileIconKind, Glyph>> = {
  js: { text: 'JS', size: 9 },
  ts: { text: 'TS', size: 9 },
  vue: { path: 'M2 4h4l6 10 6-10h4L12 21z' },
  go: { text: 'GO', size: 8 },
  css: { text: '#', size: 12 },
  json: { text: '{}', size: 10.5 },
  rs: { text: 'RS', size: 9 },
  html: { path: 'M8 7l-5 5 5 5M16 7l5 5-5 5' },
  md: { path: 'M3 17V7l4 5 4-5v10M17 7v8M14 12.5l3 3 3-3' },
  test: { path: 'M9 3h6M10 3v6l-5 9a2 2 0 0 0 1.7 3h10.6a2 2 0 0 0 1.7-3l-5-9V3M7.5 15h9' },
  img: { path: 'M4 5h16v14H4zM4 16l5-5 4 4 2-2 5 5M15.5 9.5h.01' },
  sh: { path: 'M4 6l5 6-5 6M12 18h8' },
  // A toothed cog, the same outline as the Settings icon (a thinner sun shape read as a speck at 14px).
  cfg: {
    path: 'M12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6zM19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z',
  },
  folder: { path: 'M3 6h6l2 2h10v11H3z' },
  file: { path: 'M6 3h9l4 4v14H6zM14 3v5h5' },
}

/** An SVG file name, or a light/dark pair when the set ships both. */
type SvgEntry = string | { light: string; dark: string }

export interface FileIconSet {
  id: FileIconSetId
  /** Display name; the two Seelegent sets and "off" are translated instead (settings.fileIcons.*). */
  name: string
  kind: 'builtin' | 'svg' | 'off'
  mono?: boolean
  license?: string
  source?: string
  /** Per-flavour folders (Catppuccin); the flavour follows the theme: Latte on light, Mocha on dark. */
  flavors?: Readonly<Record<ThemeScheme, string>>
  map: Partial<Record<FileIconKind, SvgEntry>>
  names: Readonly<Record<string, SvgEntry>>
}

export const FILE_ICON_SET_IDS = [
  'seelegent',
  'seelegent-mono',
  'seti',
  'material',
  'jetbrains',
  'catppuccin',
  'off',
] as const

export type FileIconSetId = (typeof FILE_ICON_SET_IDS)[number]

export const DEFAULT_FILE_ICON_SET: FileIconSetId = 'seelegent'

// JetBrains ships a light icon "x.svg" and a dark one "x_dark.svg".
const jb = (name: string) => ({ light: `${name}.svg`, dark: `${name}_dark.svg` })

export const FILE_ICON_SETS: Readonly<Record<FileIconSetId, FileIconSet>> = {
  seelegent: { id: 'seelegent', name: 'Seelegent', kind: 'builtin', map: {}, names: {} },
  'seelegent-mono': {
    id: 'seelegent-mono',
    name: 'Seelegent mono',
    kind: 'builtin',
    mono: true,
    map: {},
    names: {},
  },
  seti: {
    id: 'seti',
    name: 'Seti',
    kind: 'svg',
    license: 'MIT',
    source: 'jesseweed/seti-ui',
    // No rs / file: Seti's rust.svg and default.svg have no fill (black, invisible on dark themes). Tests use
    // the language icon, as Seti does.
    map: {
      js: 'javascript.svg',
      ts: 'typescript.svg',
      css: 'css.svg',
      json: 'json.svg',
      md: 'markdown.svg',
      html: 'html.svg',
      test: 'javascript.svg',
      cfg: 'config.svg',
      sh: 'shell.svg',
      img: 'image.svg',
      folder: 'folder.svg',
    },
    names: {
      'package.json': 'npm.svg',
      '.gitignore': 'git.svg',
      dockerfile: 'docker.svg',
      'tsconfig.json': 'tsconfig.svg',
      '*.test.ts': 'typescript.svg',
      '*.spec.ts': 'typescript.svg',
    },
  },
  material: {
    id: 'material',
    name: 'Material',
    kind: 'svg',
    license: 'MIT',
    source: 'PKief/vscode-material-icon-theme',
    // No file / folder: Material generates file.svg and folder.svg at build time (not in its repository), so
    // plain files and folders use the Seelegent glyph; a "src/" folder gets Material's own icon.
    map: {
      js: 'javascript.svg',
      ts: 'typescript.svg',
      css: 'css.svg',
      json: 'json.svg',
      md: 'markdown.svg',
      html: 'html.svg',
      test: 'test-js.svg',
      cfg: 'settings.svg',
      sh: 'console.svg',
      img: 'image.svg',
      rs: 'rust.svg',
    },
    names: {
      'package.json': 'nodejs.svg',
      '.gitignore': 'git.svg',
      'readme.md': 'readme.svg',
      dockerfile: 'docker.svg',
      'tsconfig.json': 'tsconfig.svg',
      '*.test.ts': 'test-ts.svg',
      '*.spec.ts': 'test-ts.svg',
      'src/': 'folder-src.svg',
    },
  },
  jetbrains: {
    id: 'jetbrains',
    name: 'JetBrains',
    kind: 'svg',
    license: 'Apache-2.0',
    source: 'JetBrains/intellij-community @ ad5656c6',
    // IntelliJ Community has no TypeScript or Rust file icon (those live in closed plugins): they use anyType.
    map: {
      js: jb('javaScript'),
      css: jb('css'),
      json: jb('json'),
      md: jb('markdown'),
      html: jb('html'),
      test: jb('test'),
      cfg: jb('config'),
      sh: jb('shell'),
      img: jb('image'),
      folder: jb('folder'),
      file: jb('anyType'),
    },
    names: { '.gitignore': 'gitignore.svg' },
  },
  catppuccin: {
    id: 'catppuccin',
    name: 'Catppuccin',
    kind: 'svg',
    license: 'MIT',
    source: 'catppuccin/vscode-icons',
    flavors: { light: 'latte', dark: 'mocha' },
    map: {
      js: 'javascript.svg',
      ts: 'typescript.svg',
      css: 'css.svg',
      json: 'json.svg',
      md: 'markdown.svg',
      html: 'html.svg',
      test: 'javascript-test.svg',
      cfg: 'config.svg',
      sh: 'bash.svg',
      img: 'image.svg',
      rs: 'rust.svg',
      folder: '_folder.svg',
      file: '_file.svg',
    },
    names: {
      'package.json': 'package-json.svg',
      '.gitignore': 'git.svg',
      'readme.md': 'readme.svg',
      dockerfile: 'docker.svg',
      'tsconfig.json': 'typescript-config.svg',
      '*.test.ts': 'typescript-test.svg',
      '*.spec.ts': 'typescript-test.svg',
      'src/': 'folder_src.svg',
    },
  },
  off: { id: 'off', name: 'Off', kind: 'off', map: {}, names: {} },
}

export function isFileIconSet(value: unknown): value is FileIconSetId {
  return typeof value === 'string' && (FILE_ICON_SET_IDS as readonly string[]).includes(value)
}

/** Values the old dashboard saved before icon sets existed. */
const LEGACY_SETS: Readonly<Record<string, FileIconSetId>> = {
  color: 'seelegent',
  mono: 'seelegent-mono',
}

/** A saved value (new or from the old dashboard) as a set id, or null when it means nothing. */
export function normalizeFileIconSet(value: unknown): FileIconSetId | null {
  if (isFileIconSet(value)) return value
  if (typeof value === 'string' && value in LEGACY_SETS) return LEGACY_SETS[value] ?? null
  return null
}

/** Where public/fileicons/ is served (the app's base URL, then fileicons/). */
export const FILEICONS_URL = `${import.meta.env.BASE_URL.replace(/\/$/, '')}/fileicons`

const EXT_KIND: Readonly<Record<string, FileIconKind>> = {
  js: 'js',
  mjs: 'js',
  cjs: 'js',
  ts: 'ts',
  tsx: 'ts',
  mts: 'ts',
  cts: 'ts',
  vue: 'vue',
  go: 'go',
  css: 'css',
  json: 'json',
  rs: 'rs',
  html: 'html',
  htm: 'html',
  md: 'md',
  png: 'img',
  jpg: 'img',
  jpeg: 'img',
  svg: 'img',
  gif: 'img',
  webp: 'img',
  sh: 'sh',
  cmd: 'sh',
  ps1: 'sh',
  bat: 'sh',
}

const TEST_RE = /\.(test|spec)\.[a-z]+$|_test\.go$/
// Config files (gear): *config.json (tsconfig.json too), TOML/YAML, .env*, package.json and tool dotfiles.
// package-lock.json is generated data, not config: it keeps the JSON glyph.
const CFG_RE =
  /config\.json$|\.toml$|\.ya?ml$|^\.env|^package\.json$|^go\.mod$|^\.(git\w*|editorconfig|npmrc|nvmrc|prettierrc|eslintrc)/

// Every special name any set knows: exact names, then "*.x" suffixes (longest first).
const SPECIAL_EXACT = new Set<string>()
const SPECIAL_SUFFIX: string[] = []
for (const set of Object.values(FILE_ICON_SETS)) {
  for (const key of Object.keys(set.names)) {
    if (!key.startsWith('*')) SPECIAL_EXACT.add(key)
    else if (!SPECIAL_SUFFIX.includes(key)) SPECIAL_SUFFIX.push(key)
  }
}
SPECIAL_SUFFIX.sort((a, b) => b.length - a.length)

function cleanPath(path: string): string {
  // Paths from git or logs may carry a trailing CR/space or git's "quoted" form.
  return path
    .trim()
    .replace(/^"(.*)"$/, '$1')
    .replace(/\\/g, '/')
}

/** The kind of a path: 'folder' when it ends with '/', then test, config, by extension, else 'file'. */
export function fileIconKind(path: string): FileIconKind {
  const p = cleanPath(path)
  if (p.endsWith('/')) return 'folder'
  const name = (p.split('/').pop() ?? '').toLowerCase()
  if (TEST_RE.test(name)) return 'test'
  if (CFG_RE.test(name)) return 'cfg'
  const dot = name.lastIndexOf('.')
  if (dot > 0) return EXT_KIND[name.slice(dot + 1)] ?? 'file'
  return 'file'
}

/** The special-name key a path matches in any set ('package.json', '*.test.ts', 'src/'…), or ''. */
export function fileIconSpecialName(path: string): string {
  const p = cleanPath(path)
  const isDir = p.endsWith('/')
  const base = (p.replace(/\/+$/, '').split('/').pop() ?? '').toLowerCase()
  if (!base) return ''
  const key = isDir ? `${base}/` : base
  if (SPECIAL_EXACT.has(key)) return key
  if (isDir) return ''
  return (
    SPECIAL_SUFFIX.find(
      (suffix) => base.length >= suffix.length && base.endsWith(suffix.slice(1)),
    ) ?? ''
  )
}

function pick(entry: SvgEntry | undefined, scheme: ThemeScheme): string | null {
  if (!entry) return null
  return typeof entry === 'string' ? entry : entry[scheme]
}

/** What to draw for one path. `null` = draw nothing (the "off" set). */
export interface ResolvedFileIcon {
  kind: FileIconKind
  set: FileIconSetId
  /** URL of the set's SVG, or null when the Seelegent glyph is drawn. */
  src: string | null
  glyph: Glyph
  mono: boolean
}

/**
 * The icon for `path` (a trailing '/' means a folder) in `set`, for a light or dark theme (JetBrains ships
 * light and dark files; Catppuccin draws Latte on light themes and Mocha on dark ones).
 */
export function fileIcon(
  path: string,
  set: FileIconSetId,
  scheme: ThemeScheme = 'dark',
): ResolvedFileIcon | null {
  const info = FILE_ICON_SETS[set]
  if (info.kind === 'off') return null
  const kind = fileIconKind(path)
  const resolved = { kind, set, src: null, glyph: GLYPHS[kind], mono: Boolean(info.mono) }
  if (info.kind !== 'svg') return resolved
  const special = fileIconSpecialName(path)
  let file = special ? pick(info.names[special], scheme) : null
  file ??= pick(info.map[kind], scheme)
  if (!file && kind !== 'folder') file = pick(info.map.file, scheme)
  if (!file) return resolved
  const dir = info.flavors ? `${info.id}/${info.flavors[scheme]}` : info.id
  return { ...resolved, src: `${FILEICONS_URL}/${dir}/${file}` }
}

/** Every SVG file a set can draw, as paths under public/fileicons/ (for the asset test). */
export function fileIconAssets(set: FileIconSetId): string[] {
  const info = FILE_ICON_SETS[set]
  if (info.kind !== 'svg') return []
  const entries = [...Object.values(info.map), ...Object.values(info.names)]
  const files = entries.flatMap((entry) =>
    typeof entry === 'string' ? [entry] : [entry.light, entry.dark],
  )
  const dirs = info.flavors ? Object.values(info.flavors).map((f) => `${info.id}/${f}`) : [info.id]
  return [...new Set(dirs.flatMap((dir) => files.map((file) => `${dir}/${file}`)))]
}
