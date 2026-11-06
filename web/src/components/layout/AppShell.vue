<script setup lang="ts">
import { computed, nextTick, provide, shallowRef, watch, watchEffect } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterView, useRoute, useRouter, type RouteLocationNormalizedLoaded } from 'vue-router'

import ToastContainer from '@/components/ui/ToastContainer.vue'
import { useAppCommands } from '@/composables/useAppCommands'
import { useBreakpoint } from '@/composables/useMediaQuery'
import { useShortcutDispatcher } from '@/composables/useShortcuts'
import CommandPalette from '@/features/palette/CommandPalette.vue'
import SettingsDialog from '@/features/settings/SettingsDialog.vue'

import BottomNav from './BottomNav.vue'
import ConnectionBanner from './ConnectionBanner.vue'
import NavRail from './NavRail.vue'
import { DEFAULT_PAGE_WIDTH, PAGE_WIDTH_KEY } from './pageWidth'
import TopAppBar from './TopAppBar.vue'

/**
 * The app frame: navigation (rail from 600px, top bar + bottom bar on phones), the connection banner, the
 * workspace that renders the current page, and the app-wide overlays (settings, palette, toasts).
 *
 * Overlay routes (settings) do not replace the page: the workspace keeps rendering the last normal route, and on
 * a cold open of an overlay URL it renders Watch.
 */
const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const { tier, isMobile } = useBreakpoint()

const lastPage = shallowRef<RouteLocationNormalizedLoaded | null>(null)
watch(
  () => route.fullPath,
  () => {
    if (!route.meta.overlay) lastPage.value = router.currentRoute.value
  },
  { immediate: true },
)
// A resolved location carries everything RouterView reads (matched, params, meta); its type differs only in
// allowing a null name, and '/' is named.
const home = router.resolve('/') as unknown as RouteLocationNormalizedLoaded
const background = computed(() => lastPage.value ?? home)
const activeNav = computed(() => background.value.meta.nav)
// The width contract follows the page on screen, so it does not change while settings is open over it.
const pageWidth = computed(() => background.value.meta.width ?? DEFAULT_PAGE_WIDTH)
provide(PAGE_WIDTH_KEY, pageWidth)

useShortcutDispatcher()
useAppCommands()

watchEffect(() => {
  const key = route.meta.titleKey
  const title = key ? t(key) : ''
  document.title = title ? `${title} · Seelegent` : 'Seelegent'
})

const workspace = shallowRef<HTMLElement | null>(null)
watch(
  () => background.value.path,
  () =>
    void nextTick(() => {
      if (workspace.value) workspace.value.scrollTop = 0
    }),
)
</script>

<template>
  <div class="shell" :class="`shell--${tier}`">
    <a class="skip-link" href="#main">{{ t('common.ui.skipToContent') }}</a>
    <TopAppBar v-if="isMobile" :active="activeNav" />
    <NavRail v-else :active="activeNav" :compact="tier === 'compact'" />
    <div class="shell__main">
      <ConnectionBanner />
      <main id="main" ref="workspace" class="workspace" tabindex="-1" :data-page-width="pageWidth">
        <RouterView :route="background" />
      </main>
    </div>
    <BottomNav v-if="isMobile" :active="activeNav" />
    <SettingsDialog />
    <CommandPalette />
    <ToastContainer />
  </div>
</template>

<style scoped>
.shell {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  height: 100dvh;
  overflow: hidden;
  background: var(--bg-app);
}

.shell--mobile {
  grid-template-columns: minmax(0, 1fr);
  grid-template-rows: auto minmax(0, 1fr) auto;
}

.shell__main {
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
  padding: var(--workspace-inset) var(--workspace-inset) var(--workspace-inset) 0;
  gap: var(--workspace-inset);
}

.shell--mobile .shell__main {
  padding: 0;
  gap: 0;
}

/* From 600px the workspace is an inset panel on the app background (radius-lg; the inset is its margin). */
.workspace {
  flex: 1;
  min-height: 0;
  overflow: auto;
  overscroll-behavior: contain;
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
  outline: none;
}

.shell--mobile .workspace {
  border: 0;
  border-radius: 0;
}

.skip-link {
  position: fixed;
  top: var(--space-2);
  left: var(--space-2);
  z-index: var(--z-tooltip);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  background: var(--accent);
  color: var(--text-on-accent);
  font-weight: var(--fw-semibold);
  transform: translateY(-200%);
}

.skip-link:focus-visible {
  transform: none;
}
</style>
