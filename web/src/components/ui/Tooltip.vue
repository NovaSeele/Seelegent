<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'

import { useFloating, type Placement } from '@/composables/useFloating'

import Kbd from './Kbd.vue'

/**
 * A short hint shown on hover (after a delay) and on keyboard focus, hidden by Esc, pointer leave or blur, and by
 * a scroll that moves its trigger (it would point at the wrong place; a log scrolling elsewhere leaves it be).
 * Not shown for touch. It stays unseen until useFloating has placed it. With `describe`, the hint is linked to the trigger via aria-describedby; leave it off
 * when the text repeats the trigger's accessible name (icon buttons already carry it as aria-label).
 */
const props = withDefaults(
  defineProps<{
    text: string
    shortcut?: string
    placement?: Placement
    describe?: boolean
    delay?: number
    disabled?: boolean
  }>(),
  { shortcut: undefined, placement: 'top', delay: 450 },
)

const id = useId()
const anchor = ref<HTMLElement | null>(null)
const tip = ref<HTMLElement | null>(null)
const visible = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined

const placementRef = computed(() => props.placement)
const { style } = useFloating(anchor, tip, visible, {
  placement: placementRef,
  offset: 6,
  onLost: () => hide(),
})

function show(after: number) {
  if (props.disabled || !props.text) return
  clearTimeout(timer)
  timer = setTimeout(() => (visible.value = true), after)
}

function hide() {
  clearTimeout(timer)
  visible.value = false
}

function onPointerEnter(event: PointerEvent) {
  if (event.pointerType !== 'touch') show(props.delay)
}

function onFocusIn(event: FocusEvent) {
  const target = event.target
  if (target instanceof HTMLElement && target.matches(':focus-visible')) show(0)
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && visible.value) {
    hide()
    event.stopPropagation()
  }
}

onMounted(() => {
  if (!props.describe) return
  const trigger = anchor.value?.querySelector<HTMLElement>('button, a, input, [tabindex]')
  trigger?.setAttribute('aria-describedby', id)
})

let shownAt: { top: number; left: number } | null = null

function onScroll() {
  const rect = anchor.value?.getBoundingClientRect()
  if (!rect || !shownAt) return
  if (Math.abs(rect.top - shownAt.top) > 1 || Math.abs(rect.left - shownAt.left) > 1) hide()
}

watch(visible, (isVisible) => {
  if (isVisible) {
    const rect = anchor.value?.getBoundingClientRect()
    shownAt = rect ? { top: rect.top, left: rect.left } : null
    window.addEventListener('scroll', onScroll, { capture: true, passive: true })
  } else {
    shownAt = null
    window.removeEventListener('scroll', onScroll, { capture: true })
  }
})

onBeforeUnmount(() => {
  clearTimeout(timer)
  window.removeEventListener('scroll', onScroll, { capture: true })
})
</script>

<template>
  <span
    ref="anchor"
    class="tooltip-anchor"
    @pointerenter="onPointerEnter"
    @pointerleave="hide"
    @pointerdown="hide"
    @focusin="onFocusIn"
    @focusout="hide"
    @keydown="onKeydown"
  >
    <slot />
    <Teleport to="body">
      <div
        v-if="visible || describe"
        v-show="visible"
        :id="id"
        ref="tip"
        role="tooltip"
        class="tooltip"
        :style="style"
      >
        <span>{{ text }}</span>
        <Kbd v-if="shortcut" :keys="shortcut" size="sm" />
      </div>
    </Teleport>
  </span>
</template>

<style scoped>
.tooltip-anchor {
  display: inline-flex;
}

.tooltip {
  z-index: var(--z-tooltip);
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  max-width: 280px;
  padding: var(--space-1) var(--space-2);
  border: var(--border-width) solid var(--border-strong);
  border-radius: var(--radius-sm);
  background: var(--bg-elevated);
  box-shadow: var(--shadow-md);
  color: var(--text-primary);
  font-size: var(--fs-xs);
  line-height: var(--lh-tight);
  pointer-events: none;
  animation: tooltip-in var(--duration-fast) var(--ease-out);
}

@keyframes tooltip-in {
  from {
    opacity: 0;
  }
}
</style>
