/**
 * Theme registry. Each id has a palette file in src/styles/themes/<id>.css (a unit test keeps the two in step);
 * the pre-paint script in index.html gets the list of ids from those file names at build time.
 * Names are proper nouns and are not translated; group labels are (settings.appearance.themeGroup.*).
 */
export type ThemeScheme = 'light' | 'dark'
export type ThemeGroup = 'seelegent' | 'claude' | 'codex' | 'jetbrains' | 'vscode' | 'popular'

export interface ThemeInfo {
  readonly id: string
  readonly name: string
  readonly group: ThemeGroup
  readonly scheme: ThemeScheme
}

export const THEMES = [
  { id: 'seelegent-dark', name: 'Seelegent Ink', group: 'seelegent', scheme: 'dark' },
  { id: 'seelegent-light', name: 'Seelegent Paper', group: 'seelegent', scheme: 'light' },
  { id: 'claude-dark', name: 'Claude Dark', group: 'claude', scheme: 'dark' },
  { id: 'claude-light', name: 'Claude Light', group: 'claude', scheme: 'light' },
  { id: 'codex-dark', name: 'Codex Dark', group: 'codex', scheme: 'dark' },
  { id: 'codex-light', name: 'Codex Light', group: 'codex', scheme: 'light' },
  { id: 'jetbrains-islands-dark', name: 'JetBrains New UI', group: 'jetbrains', scheme: 'dark' },
  { id: 'jetbrains-darcula', name: 'JetBrains Darcula', group: 'jetbrains', scheme: 'dark' },
  { id: 'jetbrains-light', name: 'IntelliJ Light', group: 'jetbrains', scheme: 'light' },
  { id: 'vscode-dark-modern', name: 'VS Code Dark Modern', group: 'vscode', scheme: 'dark' },
  { id: 'vscode-dark-plus', name: 'VS Code Dark+', group: 'vscode', scheme: 'dark' },
  { id: 'vscode-light-modern', name: 'VS Code Light Modern', group: 'vscode', scheme: 'light' },
  { id: 'github-dark', name: 'GitHub Dark', group: 'popular', scheme: 'dark' },
  { id: 'github-light', name: 'GitHub Light', group: 'popular', scheme: 'light' },
  { id: 'dracula', name: 'Dracula', group: 'popular', scheme: 'dark' },
  { id: 'catppuccin-mocha', name: 'Catppuccin Mocha', group: 'popular', scheme: 'dark' },
  { id: 'catppuccin-latte', name: 'Catppuccin Latte', group: 'popular', scheme: 'light' },
  { id: 'gruvbox-dark', name: 'Gruvbox Dark Soft', group: 'popular', scheme: 'dark' },
  { id: 'nord', name: 'Nord', group: 'popular', scheme: 'dark' },
  { id: 'tokyo-night', name: 'Tokyo Night', group: 'popular', scheme: 'dark' },
  { id: 'one-dark', name: 'One Dark', group: 'popular', scheme: 'dark' },
  { id: 'monokai', name: 'Monokai', group: 'popular', scheme: 'dark' },
  { id: 'solarized-dark', name: 'Solarized Dark', group: 'popular', scheme: 'dark' },
  { id: 'solarized-light', name: 'Solarized Light', group: 'popular', scheme: 'light' },
  { id: 'synthwave-84', name: "Synthwave '84", group: 'popular', scheme: 'dark' },
  { id: 'everforest-light', name: 'Everforest Light', group: 'popular', scheme: 'light' },
] as const satisfies readonly ThemeInfo[]

export type ThemeId = (typeof THEMES)[number]['id']
/** What the user picked: a theme, or "auto" (Seelegent Paper / Ink following the OS colour scheme). */
export type ThemeChoice = ThemeId | 'auto'

export const THEME_GROUPS: readonly ThemeGroup[] = [
  'seelegent',
  'claude',
  'codex',
  'jetbrains',
  'vscode',
  'popular',
]

export const AUTO_THEMES: Readonly<Record<ThemeScheme, ThemeId>> = {
  light: 'seelegent-light',
  dark: 'seelegent-dark',
}

const themeIds: ReadonlySet<string> = new Set(THEMES.map((theme) => theme.id))

export function isThemeId(value: unknown): value is ThemeId {
  return typeof value === 'string' && themeIds.has(value)
}

export function isThemeChoice(value: unknown): value is ThemeChoice {
  return value === 'auto' || isThemeId(value)
}

export function resolveTheme(choice: ThemeChoice, prefersDark: boolean): ThemeId {
  if (choice !== 'auto') return choice
  return prefersDark ? AUTO_THEMES.dark : AUTO_THEMES.light
}

export function themeInfo(id: ThemeId): ThemeInfo {
  const found = THEMES.find((theme) => theme.id === id)
  if (!found) throw new Error(`unknown theme ${id}`)
  return found
}

/** UI font stacks (tokens.css --font-ui-<id>). Only locally installed fonts are used; nothing is downloaded. */
export const UI_FONTS = ['system', 'inter', 'ibm-plex-sans', 'roboto', 'arial'] as const
export type UiFont = (typeof UI_FONTS)[number]

/** Monospace stacks (tokens.css --font-mono-<id>); "theme" follows the active theme's recommendation. */
export const MONO_FONTS = [
  'theme',
  'cascadia-code',
  'cascadia-mono',
  'jetbrains-mono',
  'fira-code',
  'consolas',
  'source-code-pro',
  'ibm-plex-mono',
  'iosevka',
  'menlo',
] as const
export type MonoFont = (typeof MONO_FONTS)[number]

/** Display names of the font ids that are proper nouns ("system" and "theme" are translated). */
export const FONT_NAMES: Readonly<Record<Exclude<UiFont | MonoFont, 'system' | 'theme'>, string>> =
  {
    inter: 'Inter',
    'ibm-plex-sans': 'IBM Plex Sans',
    roboto: 'Roboto',
    arial: 'Arial',
    'cascadia-code': 'Cascadia Code',
    'cascadia-mono': 'Cascadia Mono',
    'jetbrains-mono': 'JetBrains Mono',
    'fira-code': 'Fira Code',
    consolas: 'Consolas',
    'source-code-pro': 'Source Code Pro',
    'ibm-plex-mono': 'IBM Plex Mono',
    iosevka: 'Iosevka',
    menlo: 'Menlo',
  }

export const MOTION_PREFS = ['system', 'on', 'off'] as const
export type MotionPref = (typeof MOTION_PREFS)[number]
