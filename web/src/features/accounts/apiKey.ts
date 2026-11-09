/**
 * The API-key form's pure rules: the key is cleaned and checked exactly like the daemon's account.NormalizeKey
 * (so a URL, a space or a pasted .env line is caught before anything reaches the server), a key's prefix names its
 * service, and the services are offered in one order with plain names.
 */
import type { Provider, ProviderPreset } from '@/api/types'

/** Why a key is refused: the daemon's details.reason for field apiKey / key (accounts.keyError.<reason>). */
export type KeyError = 'empty' | 'too_long' | 'url' | 'whitespace' | 'control'

/** A pasted key after cleaning, with what the cleaning did and what is still wrong with it. */
export interface KeyCheck {
  /** The key to send: trimmed, without a leading NAME= and surrounding quotes. */
  key: string
  error?: KeyError
  /** The variable name removed from the start (LLM_API_KEY for "LLM_API_KEY=sk-…"), or ''. */
  removedName: string
  quotesRemoved: boolean
  /** Kept whole but reads as name=value (lower-case names are not stripped): advice only. */
  assignment: boolean
}

/** The daemon's limits (maxKeyLen, minKeyAfterName, maxNoticeName) and patterns (envAssignment, looksAssigned). */
export const MAX_KEY_BYTES = 512
const MIN_KEY_AFTER_NAME = 8
/** The daemon's maxNoticeName: a longer removed name is not quoted back. */
export const MAX_NOTICE_NAME = 40
const ENV_ASSIGNMENT = /^(?:export[\t\n\f\r ]+)?([A-Z_][A-Z0-9_]*)[\t\n\f\r ]*=[\t\n\f\r ]*/
const LOOKS_ASSIGNED = /^[A-Za-z_][A-Za-z0-9_]*=[^=]/
/** Go's unicode.IsSpace, as a character class. */
const SPACES =
  '\\t\\n\\v\\f\\r \\u0085\\u00a0\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000'
const SPACE = new RegExp(`[${SPACES}]`)
/** Go's unicode.IsControl (category Cc). */
// eslint-disable-next-line no-control-regex
const CONTROL = /[\u0000-\u001f\u007f-\u009f]/
const EDGE_SPACES = new RegExp(`^[${SPACES}]+|[${SPACES}]+$`, 'g')
/** Go's strings.TrimSpace: every unicode.IsSpace character at both ends. */
const trimSpace = (s: string) => s.replace(EDGE_SPACES, '')

const bytes = (s: string) => new TextEncoder().encode(s).length

export function checkKey(raw: string): KeyCheck {
  let key = trimSpace(raw)
  let removedName = ''
  let quotesRemoved = false
  const m = ENV_ASSIGNMENT.exec(key)
  if (m) {
    const rest = trimSpace(key.slice(m[0].length))
    if (bytes(rest) >= MIN_KEY_AFTER_NAME && !rest.startsWith('=')) {
      key = rest
      removedName = m[1] ?? ''
    }
  }
  const first = key[0]
  if (key.length >= 2 && (first === '"' || first === "'" || first === '`') && key.endsWith(first)) {
    key = trimSpace(key.slice(1, -1))
    quotesRemoved = true
  }
  const error = keyError(key)
  const eq = key.indexOf('=')
  const assignment =
    !error && LOOKS_ASSIGNED.test(key) && bytes(key) - bytes(key.slice(0, eq)) > MIN_KEY_AFTER_NAME
  return { key, removedName, quotesRemoved, assignment, ...(error ? { error } : {}) }
}

function keyError(key: string): KeyError | undefined {
  if (key === '') return 'empty'
  if (bytes(key) > MAX_KEY_BYTES) return 'too_long'
  if (key.includes('://')) return 'url'
  if (SPACE.test(key)) return 'whitespace'
  if (CONTROL.test(key)) return 'control'
  return undefined
}

/** Key prefixes that name one service for sure (the daemon's keyPrefixes); OpenAI's "sk-" is shared by many. */
const KEY_PREFIXES: readonly (readonly [prefix: string, preset: string])[] = [
  ['sk-ant-', 'anthropic'],
  ['sk-or-', 'openrouter'],
  ['gsk_', 'groq'],
]

/** The preset a (cleaned) key's prefix names, or null. */
export function detectPreset(key: string): string | null {
  return KEY_PREFIXES.find(([prefix]) => key.startsWith(prefix))?.[1] ?? null
}

/** The order the presets are offered in (design: the well-known services, then the two "Other" ones). */
export const PRESET_ORDER = [
  'anthropic',
  'deepseek',
  'moonshot',
  'zai',
  'openrouter',
  'openai',
  'groq',
  'ollama',
  'custom-anthropic',
  'custom-openai',
] as const

export function orderedPresets(presets: readonly ProviderPreset[]): ProviderPreset[] {
  const rank = (id: string) => {
    const i = (PRESET_ORDER as readonly string[]).indexOf(id)
    return i < 0 ? PRESET_ORDER.length : i
  }
  return [...presets].sort((a, b) => rank(a.id) - rank(b.id) || a.name.localeCompare(b.name))
}

/** A hand-made service: its base URL is the user's to give. */
export const isCustomPreset = (id: string | undefined): boolean => !!id?.startsWith('custom-')

/** The service a form names: one of the user's providers, or a preset a new provider is made from. */
export type ServiceRef = { kind: 'provider'; id: string } | { kind: 'preset'; id: string }

export const serviceValue = (ref: ServiceRef): string =>
  ref.kind === 'provider' ? `provider:${ref.id}` : `preset:${ref.id}`

export function parseService(value: string): ServiceRef | null {
  const [kind, ...rest] = value.split(':')
  const id = rest.join(':')
  if (!id) return null
  if (kind === 'provider' || kind === 'preset') return { kind, id }
  return null
}

/**
 * The service a pasted key points at: the user's own enabled service made from that preset when there is one (a
 * second key joins it), else the preset.
 */
export function serviceForPreset(preset: string, providers: readonly Provider[]): ServiceRef {
  const own = providers.find((p) => p.enabled && p.preset === preset)
  return own ? { kind: 'provider', id: own.id } : { kind: 'preset', id: preset }
}
