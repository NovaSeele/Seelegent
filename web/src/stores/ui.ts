/**
 * Per-viewer UI preferences (appearance, language, layout). Each one is saved under its own storage key
 * (config/prefs.ts) and applied to <html> the same way the pre-paint script does, so a change shows at once.
 */
import { defineStore } from 'pinia'
import { computed, ref, watch, watchEffect, type Ref } from 'vue'

import { MEDIA } from '@/config/breakpoints'
import { PREFS, RANGES } from '@/config/prefs'
import {
  resolveTheme,
  type MonoFont,
  type MotionPref,
  type ThemeChoice,
  type UiFont,
} from '@/config/themes'
import { useMediaQuery } from '@/composables/useMediaQuery'
import { activateLocale, detectLocale, type Locale } from '@/i18n'
import { readStored, writeStored, type StoredValue } from '@/lib/storage'

function persisted<T>(spec: StoredValue<T>): Ref<T> {
  const value = ref(readStored(spec)) as Ref<T>
  watch(value, (next) => writeStored(spec, next))
  return value
}

export function browserLocale(): Locale {
  return detectLocale(typeof navigator === 'undefined' ? [] : (navigator.languages ?? []))
}

export const useUiStore = defineStore('ui', () => {
  const theme = persisted(PREFS.theme)
  const fontUi = persisted(PREFS.fontUi)
  const fontMono = persisted(PREFS.fontMono)
  const sizeUi = persisted(PREFS.sizeUi)
  const sizeTerm = persisted(PREFS.sizeTerm)
  const lineHeightTerm = persisted(PREFS.lineHeightTerm)
  const motion = persisted(PREFS.motion)
  const localeChoice = persisted(PREFS.locale)
  const sidebarWidth = persisted(PREFS.sidebarWidth)
  const sidebarCollapsed = persisted(PREFS.sidebarCollapsed)

  const prefersLight = useMediaQuery('(prefers-color-scheme: light)')
  const prefersReducedMotion = useMediaQuery('(prefers-reduced-motion: reduce)')
  const railUp = useMediaQuery(MEDIA.railUp)

  const resolvedTheme = computed(() => resolveTheme(theme.value, !prefersLight.value))
  const motionOff = computed(
    () => motion.value === 'off' || (motion.value === 'system' && prefersReducedMotion.value),
  )
  /** The language in use: the saved choice, else the browser's. */
  const locale = computed<Locale>(() => localeChoice.value ?? browserLocale())
  /** Collapsing the sidebar only applies where panes sit side by side. */
  const sidebarHidden = computed(() => sidebarCollapsed.value && railUp.value)

  watchEffect(() => {
    if (typeof document === 'undefined') return
    const root = document.documentElement
    root.dataset.theme = resolvedTheme.value
    root.dataset.motion = motionOff.value ? 'off' : 'on'
    if (fontUi.value === 'system') delete root.dataset.fontUi
    else root.dataset.fontUi = fontUi.value
    if (fontMono.value === 'theme') delete root.dataset.fontMono
    else root.dataset.fontMono = fontMono.value
    root.style.setProperty('--font-size-ui', `${sizeUi.value}px`)
    root.style.setProperty('--font-size-term', `${sizeTerm.value}px`)
    root.style.setProperty('--line-height-term', String(lineHeightTerm.value))
    root.style.setProperty('--sidebar-width', `${sidebarWidth.value}px`)
  })

  function setTheme(next: ThemeChoice) {
    theme.value = next
  }
  function setMotion(next: MotionPref) {
    motion.value = next
  }
  function setFontUi(next: UiFont) {
    fontUi.value = next
  }
  function setFontMono(next: MonoFont) {
    fontMono.value = next
  }

  /** Switches the UI language without a reload; null follows the browser again. */
  async function setLocale(next: Locale | null) {
    localeChoice.value = next
    await activateLocale(locale.value)
  }

  function setSidebarWidth(width: number) {
    const { min, max } = RANGES.sidebarWidth
    sidebarWidth.value = Math.round(Math.min(max, Math.max(min, width)))
  }

  function toggleSidebar(force?: boolean) {
    sidebarCollapsed.value = force ?? !sidebarCollapsed.value
  }

  function resetAppearance() {
    theme.value = PREFS.theme.fallback
    fontUi.value = PREFS.fontUi.fallback
    fontMono.value = PREFS.fontMono.fallback
    sizeUi.value = PREFS.sizeUi.fallback
    sizeTerm.value = PREFS.sizeTerm.fallback
    lineHeightTerm.value = PREFS.lineHeightTerm.fallback
    motion.value = PREFS.motion.fallback
  }

  return {
    theme,
    fontUi,
    fontMono,
    sizeUi,
    sizeTerm,
    lineHeightTerm,
    motion,
    localeChoice,
    sidebarWidth,
    sidebarCollapsed,
    resolvedTheme,
    motionOff,
    locale,
    sidebarHidden,
    setTheme,
    setMotion,
    setFontUi,
    setFontMono,
    setLocale,
    setSidebarWidth,
    toggleSidebar,
    resetAppearance,
  }
})
