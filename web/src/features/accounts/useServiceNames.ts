import { useI18n } from 'vue-i18n'

import type { Provider, ProviderPreset } from '@/api/types'
import { useProvidersStore } from '@/stores/providers'

import type { ServiceRef } from './apiKey'

/** What a service is called and which CLI it runs through, for a form or a line of text. */
export interface ServiceInfo {
  name: string
  backend: Provider['backend']
  provider?: Provider
  preset?: ProviderPreset
}

/**
 * Service names as the user reads them: a provider by its own name, a preset by its name, except the two
 * hand-made ones, which read "Other (Anthropic-compatible)" and "Other (OpenAI-compatible)".
 */
export function useServiceNames() {
  const { t } = useI18n()
  const providers = useProvidersStore()

  function preset(p: ProviderPreset): string {
    if (p.id === 'custom-anthropic') return t('accounts.service.otherAnthropic')
    if (p.id === 'custom-openai') return t('accounts.service.otherOpenai')
    return p.name
  }

  function of(ref: ServiceRef): ServiceInfo | null {
    if (ref.kind === 'provider') {
      const found = providers.items.find((p) => p.id === ref.id)
      return found ? { name: found.name, backend: found.backend, provider: found } : null
    }
    const found = providers.presets.find((p) => p.id === ref.id)
    return found ? { name: preset(found), backend: found.backend, preset: found } : null
  }

  return { preset, of }
}
