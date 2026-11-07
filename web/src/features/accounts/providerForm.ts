/** Pure helpers of the provider dialog: the models text, the create body and the edit body. */
import type { ProviderChanges } from '@/api/providers'
import type { Provider, ProviderInput, ProviderPreset } from '@/api/types'

/** "a, b  c" → ['a', 'b', 'c']: models are names without spaces, split on commas or whitespace. */
export function parseModels(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((m) => m.trim())
    .filter(Boolean)
}

/**
 * The models a provider allows, for showing and editing: its `models`, else the list an exact allowedModels
 * pattern names. The daemon stores a list given on save only as that pattern (`^(?:a|b)$`, regexp.QuoteMeta per
 * model) and answers `models` with the tier models, so the list is read back from the pattern. `null` when the
 * pattern is something else (a hand-written regular expression).
 */
export function providerModels(p: Pick<Provider, 'models' | 'allowedModels'>): string[] | null {
  if (p.models.length) return [...p.models]
  if (!p.allowedModels) return []
  const exact = /^\^\(\?:(.+)\)\$$/.exec(p.allowedModels)
  if (!exact?.[1]) return null
  const parts = exact[1].split(/(?<!\\)\|/)
  // Every regexp metacharacter of a quoted model is escaped; an unescaped one means a real pattern.
  if (parts.some((part) => /(^|[^\\])[.*+?^${}()|[\]]/.test(part))) return null
  return parts.map((part) => part.replace(/\\(.)/g, '$1'))
}

/** The POST /providers body; `preset` names the preset the form was filled from, so the daemon keeps it. */
export function providerInput(
  form: ProviderPreset,
  models: string[],
  preset: string,
): ProviderInput {
  const { id, name, backend, baseUrl, enabled } = form
  return { id, name, backend, baseUrl, enabled, models, ...(preset ? { preset } : {}) }
}

/**
 * The PATCH /providers/{id} body: only the fields the user changed. Sending `models` makes the daemon rewrite the
 * provider's allowedModels to exactly that list, so an untouched models field must stay out of the body (it would
 * replace a hand-written pattern, or wipe the list when the form could not show it).
 */
export function providerChanges(
  before: Provider,
  form: ProviderPreset,
  models: string[],
): ProviderChanges {
  const changes: ProviderChanges = {}
  if (form.name !== before.name) changes.name = form.name
  if (!before.official && form.baseUrl !== before.baseUrl) changes.baseUrl = form.baseUrl
  if (form.enabled !== before.enabled) changes.enabled = form.enabled
  const current = providerModels(before) ?? []
  if (models.join('\n') !== current.join('\n')) changes.models = models
  return changes
}
