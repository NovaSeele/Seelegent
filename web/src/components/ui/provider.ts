/**
 * Provider identity: which hue token a worker backend takes (styles/providers.css, tokens.css). Colour follows the
 * backend: an API-key account (DeepSeek, Kimi, GLM, OpenRouter, …) takes the hue of the backend it runs on and
 * shows its provider's name in a neutral chip; third-party providers get no hue of their own.
 *
 * The hue is an identity mark only (a dot, a 3px edge, a -soft chip background), always next to the backend's
 * name, never text colour, never a status. Components draw it through ProviderMark.vue, or through
 * providerEdge() for the left edge of a row or card; nothing else colours by provider.
 */
export const PROVIDER_IDS = ['agy', 'claude', 'codex'] as const

export type ProviderId = (typeof PROVIDER_IDS)[number]

export const isProviderId = (backend: string | null | undefined): backend is ProviderId =>
  (PROVIDER_IDS as readonly string[]).includes(backend ?? '')

/** The hue token of a backend (or its -soft / -border step); an unknown backend takes the neutral one. */
export function providerToken(
  backend: string | null | undefined,
  step?: 'soft' | 'border',
): string {
  const id = isProviderId(backend) ? backend : 'other'
  return `var(--provider-${id}${step ? `-${step}` : ''})`
}

/**
 * The style that gives an element with the `provider-edge` class (styles/providers.css) its 3px left edge in the
 * backend's hue: `<li class="provider-edge" :style="providerEdge(account.backend)">`.
 */
export function providerEdge(backend: string | null | undefined): Record<string, string> {
  return { '--provider-edge': providerToken(backend) }
}
