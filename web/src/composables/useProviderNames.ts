import { watch } from 'vue'

import { useProvidersStore } from '@/stores/providers'

/**
 * Display names of API providers ("DeepSeek" for deepseek) for a view that shows an account's, a task's or a
 * member's provider, to pass on to ProviderMark (which stays free of stores). As soon as `ids` holds a provider,
 * the providers store reads its list and keeps it current (ensure() is idempotent), so a page opened directly,
 * such as a task link, names its provider too. Until the list arrives, and for a provider that is gone, the id
 * stands in.
 */
export function useProviderNames(
  ids: () => readonly (string | undefined)[],
): (id: string | undefined) => string | undefined {
  const providers = useProvidersStore()
  watch(
    () => ids().some(Boolean),
    (needed) => {
      if (needed) providers.ensure()
    },
    { immediate: true },
  )
  return (id) => (id ? providers.nameOf(id) : undefined)
}
