import { computed, onScopeDispose, ref, type Ref } from 'vue'

import { MEDIA, type LayoutTier } from '@/config/breakpoints'

/** Reactive `matchMedia(query).matches`; false where matchMedia is missing (tests, old browsers). */
export function useMediaQuery(query: string): Ref<boolean> {
  const matches = ref(false)
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return matches
  const list = window.matchMedia(query)
  matches.value = list.matches
  const onChange = (event: MediaQueryListEvent) => (matches.value = event.matches)
  list.addEventListener?.('change', onChange)
  onScopeDispose(() => list.removeEventListener?.('change', onChange))
  return matches
}

/** The layout tier from the shared breakpoints (config/breakpoints.ts). */
export function useBreakpoint() {
  const compactUp = useMediaQuery(MEDIA.compactUp)
  const railUp = useMediaQuery(MEDIA.railUp)
  const tier = computed<LayoutTier>(() =>
    railUp.value ? 'rail' : compactUp.value ? 'compact' : 'mobile',
  )
  return {
    tier,
    isMobile: computed(() => tier.value === 'mobile'),
    isCompact: computed(() => tier.value === 'compact'),
    isRail: computed(() => tier.value === 'rail'),
  }
}
