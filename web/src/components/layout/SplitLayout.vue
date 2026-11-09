<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import IconButton from '@/components/ui/IconButton.vue'
import { SIDEBAR_SHORTCUT } from '@/config/nav'
import { PREFS, RANGES } from '@/config/prefs'
import { useBreakpoint } from '@/composables/useMediaQuery'
import { useUiStore } from '@/stores/ui'

/**
 * List + detail layout. From 900px the sidebar (list) and the main pane sit side by side: the sidebar is
 * resizable (drag or arrow keys on the separator, 240-560px, double-click resets to 300px) and collapsible
 * (button or Mod+B). Below 900px only one pane shows: the main pane when `detailOpen`, else the sidebar.
 */
const props = defineProps<{
  /** Accessible name of the sidebar region. */
  sidebarLabel: string
  detailOpen?: boolean
}>()

const { t } = useI18n()
const ui = useUiStore()
const { isRail } = useBreakpoint()

const STEP = 16
const dragWidth = ref<number | null>(null)
const width = computed(() => dragWidth.value ?? ui.sidebarWidth)

const showSidebar = computed(() => (isRail.value ? !ui.sidebarHidden : !props.detailOpen))
const showMain = computed(() => isRail.value || !!props.detailOpen)

function clamp(value: number) {
  return Math.round(Math.min(RANGES.sidebarWidth.max, Math.max(RANGES.sidebarWidth.min, value)))
}

function onPointerDown(event: PointerEvent) {
  if (event.button !== 0) return
  const handle = event.currentTarget as HTMLElement
  const startX = event.clientX
  const startWidth = ui.sidebarWidth
  handle.setPointerCapture(event.pointerId)
  const move = (e: PointerEvent) => (dragWidth.value = clamp(startWidth + e.clientX - startX))
  const up = () => {
    if (dragWidth.value !== null) ui.setSidebarWidth(dragWidth.value)
    dragWidth.value = null
    handle.removeEventListener('pointermove', move)
    handle.removeEventListener('pointerup', up)
    handle.removeEventListener('pointercancel', up)
  }
  handle.addEventListener('pointermove', move)
  handle.addEventListener('pointerup', up)
  handle.addEventListener('pointercancel', up)
}

function onKeydown(event: KeyboardEvent) {
  const keys: Record<string, number> = {
    ArrowLeft: ui.sidebarWidth - STEP,
    ArrowRight: ui.sidebarWidth + STEP,
    Home: RANGES.sidebarWidth.min,
    End: RANGES.sidebarWidth.max,
  }
  const next = keys[event.key]
  if (next === undefined) {
    if (event.key === 'Enter') ui.toggleSidebar(true)
    return
  }
  event.preventDefault()
  ui.setSidebarWidth(next)
}
</script>

<template>
  <div
    class="split"
    :class="{ 'split--side-by-side': isRail, 'split--collapsed': isRail && ui.sidebarHidden }"
    :style="dragWidth !== null ? { '--sidebar-width': `${width}px` } : undefined"
  >
    <aside v-if="showSidebar" class="split__sidebar" :aria-label="sidebarLabel">
      <div class="split__sidebar-body"><slot name="sidebar" /></div>
      <div v-if="isRail" class="split__sidebar-foot">
        <IconButton
          icon="sidebar"
          size="sm"
          :label="t('common.ui.collapseSidebar')"
          :shortcut="SIDEBAR_SHORTCUT"
          aria-expanded="true"
          tooltip-placement="top"
          @click="ui.toggleSidebar(true)"
        />
      </div>
    </aside>
    <div v-else-if="isRail" class="split__strip">
      <IconButton
        icon="sidebar"
        size="sm"
        :label="t('common.ui.expandSidebar')"
        :shortcut="SIDEBAR_SHORTCUT"
        aria-expanded="false"
        tooltip-placement="right"
        @click="ui.toggleSidebar(false)"
      />
    </div>
    <div
      v-if="isRail && showSidebar"
      class="split__handle"
      :class="{ 'split__handle--dragging': dragWidth !== null }"
      role="separator"
      tabindex="0"
      aria-orientation="vertical"
      :aria-label="t('common.ui.resizeSidebar')"
      :aria-valuenow="width"
      :aria-valuemin="RANGES.sidebarWidth.min"
      :aria-valuemax="RANGES.sidebarWidth.max"
      @pointerdown="onPointerDown"
      @dblclick="ui.setSidebarWidth(PREFS.sidebarWidth.fallback)"
      @keydown="onKeydown"
    />
    <section v-if="showMain" class="split__main">
      <slot />
    </section>
  </div>
</template>

<style scoped>
.split {
  position: relative;
  display: flex;
  height: 100%;
  min-height: 0;
}

.split__sidebar {
  position: relative;
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 0;
  min-height: 0;
}

/* Positioned so the rows' .sr-only labels stay inside this scroll frame instead of stretching the workspace, and
   contained so a scroll that reaches the end of the list never chains into the workspace behind it. */
.split__sidebar-body {
  position: relative;
  flex: 1;
  min-height: 0;
  overflow: auto;
  overscroll-behavior: contain;
}

.split--side-by-side .split__sidebar {
  flex: none;
  width: var(--sidebar-width);
  border-right: var(--border-width) solid var(--border-subtle);
}

.split__main {
  /* Contains absolutely positioned descendants (e.g. .sr-only labels), which would otherwise overflow the
     workspace and make it scroll. */
  position: relative;
  flex: 1;
  min-width: 0;
  min-height: 0;
  overflow: auto;
}

/* An 8px grab zone centred on the sidebar border. */
.split__handle {
  position: absolute;
  top: 0;
  bottom: 0;
  left: calc(var(--sidebar-width) - 4px);
  z-index: 1;
  width: 8px;
  cursor: col-resize;
  touch-action: none;
}

.split__handle::after {
  content: '';
  position: absolute;
  top: 0;
  bottom: 0;
  left: 3px;
  width: 2px;
  background: transparent;
  transition: background-color var(--duration-fast) var(--ease-standard);
}

.split__handle:hover::after,
.split__handle--dragging::after,
.split__handle:focus-visible::after {
  background: var(--accent);
}

.split__handle:focus-visible {
  outline: none;
}

.split__sidebar-foot {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: flex-end;
  min-height: var(--pane-foot-h);
  padding: 0 var(--space-2);
  border-top: var(--border-width) solid var(--border-subtle);
}

.split__strip {
  display: flex;
  flex: none;
  justify-content: center;
  padding-top: var(--space-2);
  width: calc(var(--control-sm) + var(--space-3));
  border-right: var(--border-width) solid var(--border-subtle);
}
</style>
