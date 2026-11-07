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
 * provider's allowedModels to exactly that list, so an untouched models field must stay out of the body (the
 * daemon's Provider.models can be shorter than what the allow-list permits).
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
  if (models.join('\n') !== before.models.join('\n')) changes.models = models
  return changes
}
