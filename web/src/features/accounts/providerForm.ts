/** Pure helpers of the provider dialog: the allow-list, the create body and the edit body. */
import type { ProviderChanges } from '@/api/providers'
import type { Provider, ProviderInput, ProviderPreset, TierModels } from '@/api/types'

/** "a, b  c" → ['a', 'b', 'c']: models are names without spaces, split on commas or whitespace. */
export function parseModels(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((m) => m.trim())
    .filter(Boolean)
}

/**
 * Which models a provider allows (the contract's rules): `models` is the allow-list as last set and
 * `allowedModels` the pattern enforced for it, `^(?:a|b)$` with every model regexp-quoted. A hand-written
 * pattern that is not such a list comes with `models: []`. Both empty means any model. Tier models are allowed on
 * top in every case, and are not part of the list.
 */
export type AllowList =
  { kind: 'any' } | { kind: 'list'; models: string[] } | { kind: 'pattern'; pattern: string }

/** Go's regexp.QuoteMeta: the characters the daemon escapes in a model name. */
const quote = (model: string) => model.replace(/[\\.+*?()|[\]{}^$]/g, '\\$&')

/** The daemon's exactPattern: an anchored pattern of exactly these models ('' for none). */
export const exactPattern = (models: readonly string[]): string =>
  models.length ? `^(?:${models.map(quote).join('|')})$` : ''

/** A space, a tab or a control character: no model id has one. */
const hasSpace = (model: string) => [...model].some((ch) => (ch.codePointAt(0) ?? 0) <= 32)

/**
 * The models an exact pattern names, or null for any other pattern: the daemon's ModelsOf. Only an exact round
 * trip counts ("^(?:a.b)$" is a hand-written pattern, not the list [a.b]), and a part with a space or a control
 * character is no model id, so "^(?:a b|c)$" is hand-written too.
 */
function listOf(pattern: string): string[] | null {
  const prefix = '^(?:'
  const suffix = ')$'
  if (!pattern.startsWith(prefix) || !pattern.endsWith(suffix)) return null
  const inner = pattern.slice(prefix.length, pattern.length - suffix.length)
  if (!inner) return null
  const parts: string[] = []
  let current = ''
  let escaped = false
  for (const ch of inner) {
    if (escaped) {
      current += ch
      escaped = false
    } else if (ch === '\\') escaped = true
    else if (ch === '|') {
      parts.push(current)
      current = ''
    } else current += ch
  }
  parts.push(current)
  if (escaped || parts.some((m) => !m || hasSpace(m)) || exactPattern(parts) !== pattern)
    return null
  return parts
}

/**
 * The allow-list the models field edits. The enforced pattern decides: `models` is used when it agrees with the
 * pattern, else the list is read from the pattern. That way a daemon that still answers `models` with the tier
 * models (before the contract fix) cannot leak a tier model into the field, where saving it would replace the
 * real list.
 */
export function allowList(p: Pick<Provider, 'models' | 'allowedModels'>): AllowList {
  const pattern = p.allowedModels
  if (!pattern) return { kind: 'any' }
  if (p.models.length && exactPattern(p.models) === pattern)
    return { kind: 'list', models: [...p.models] }
  const listed = listOf(pattern)
  return listed ? { kind: 'list', models: listed } : { kind: 'pattern', pattern }
}

/** The models the list field shows: the list, or nothing for "any model" and for a hand-written pattern. */
export const listedModels = (allow: AllowList): string[] =>
  allow.kind === 'list' ? allow.models : []

/** The provider's tier models (always allowed), each once, in tier order. */
export function tierModels(p: Pick<Provider, 'tiers'>): string[] {
  const { fast, standard, strong, max } = p.tiers
  return [...new Set([fast, standard, strong, max].filter(Boolean))]
}

const sameTiers = (a: TierModels, b: TierModels) =>
  a.fast === b.fast && a.standard === b.standard && a.strong === b.strong && a.max === b.max

const hasTiers = (tiers: TierModels | undefined) => !!tiers && Object.values(tiers).some(Boolean)

/**
 * The POST /providers body; `preset` names the preset the form was filled from, so the daemon keeps it. Tier
 * models are sent when any is set (none: each task names its model).
 */
export function providerInput(
  form: ProviderPreset,
  models: string[],
  preset: string,
  tiers?: TierModels,
): ProviderInput {
  const { id, name, backend, baseUrl, enabled } = form
  return {
    id,
    name,
    backend,
    baseUrl,
    enabled,
    models,
    ...(preset ? { preset } : {}),
    ...(tiers && hasTiers(tiers) ? { tiers: { ...tiers } } : {}),
  }
}

/**
 * The PATCH /providers/{id} body: only the fields the user changed. `models` replaces the allow-list (and `[]`
 * clears it to "any model"), so it is sent only when the field differs from the list it was filled with: an
 * untouched field never replaces a hand-written pattern or the list. `allowAny` (the dialog's "Allow any model")
 * sends `models: []` for an empty field, the only way to turn a hand-written pattern back into "any model".
 * `tiers` (the tier editor) are sent when they differ from the provider's.
 */
export function providerChanges(
  before: Provider,
  form: ProviderPreset,
  models: string[],
  allowAny = false,
  tiers?: TierModels,
): ProviderChanges {
  const changes: ProviderChanges = {}
  if (tiers && !sameTiers(tiers, before.tiers)) changes.tiers = { ...tiers }
  if (form.name !== before.name) changes.name = form.name
  if (!before.official && form.baseUrl !== before.baseUrl) changes.baseUrl = form.baseUrl
  if (form.enabled !== before.enabled) changes.enabled = form.enabled
  const current = listedModels(allowList(before))
  if (models.join('\n') !== current.join('\n') || (allowAny && !models.length))
    changes.models = models
  return changes
}
