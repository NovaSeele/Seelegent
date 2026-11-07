/** Pure helpers of the provider dialog: the models text and the create body. */
import type { ProviderInput, ProviderPreset } from '@/api/types'

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
