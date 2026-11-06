<script setup lang="ts">
import { onBeforeUnmount, ref, toRef, watch } from 'vue'

import { useFloating, type Placement } from '@/composables/useFloating'
import { useLayer } from '@/composables/useLayer'
import type { LayerKind } from '@/stores/layers'

defineOptions({ inheritAttrs: false })

/**
 * A non-modal floating panel, teleported to <body> (so container queries and overflow never clip it) and
 * positioned next to `anchor`. Closes on Esc (while on top of the layer stack) and on a pointer press outside
 * the panel and the anchor. Focus handling is up to the content (Menu moves focus in; a plain popover may not).
 */
const props = withDefaults(
  defineProps<{
    open: boolean
    anchor: HTMLElement | null
    placement?: Placement
    role?: 'dialog' | 'menu' | 'listbox'
    label?: string
    id?: string
    kind?: LayerKind
  }>(),
  { placement: 'bottom-start', role: 'dialog', label: undefined, id: undefined, kind: 'popover' },
)

const emit = defineEmits<{
  'update:open': [value: boolean]
  close: [reason: 'escape' | 'outside']
}>()

const panel = ref<HTMLElement | null>(null)
const open = toRef(props, 'open')
const anchorRef = toRef(props, 'anchor')
const placement = toRef(props, 'placement')

const { style } = useFloating(anchorRef, panel, open, { placement })

function close(reason: 'escape' | 'outside') {
  emit('update:open', false)
  emit('close', reason)
}

useLayer(open, { kind: props.kind, modal: false, onEscape: () => close('escape') })

function onPointerDown(event: PointerEvent) {
  const target = event.target as Node | null
  if (!target) return
  if (panel.value?.contains(target) || props.anchor?.contains(target)) return
  close('outside')
}

watch(
  open,
  (isOpen) => {
    if (isOpen) document.addEventListener('pointerdown', onPointerDown, true)
    else document.removeEventListener('pointerdown', onPointerDown, true)
  },
  { immediate: true },
)

onBeforeUnmount(() => document.removeEventListener('pointerdown', onPointerDown, true))

defineExpose({ panel })
</script>

<template>
  <Teleport to="body">
    <Transition name="popover">
      <div
        v-if="open"
        :id="id"
        ref="panel"
        class="popover"
        :role="role"
        :aria-label="label"
        :style="style"
        v-bind="$attrs"
      >
        <slot />
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.popover {
  z-index: var(--z-popover);
  min-width: 180px;
  max-width: min(360px, calc(100vw - var(--space-4)));
  max-height: min(420px, calc(100dvh - var(--space-4)));
  overflow: auto;
  padding: var(--space-1);
  border: var(--border-width) solid var(--border-strong);
  border-radius: var(--radius-lg);
  background: var(--bg-elevated);
  box-shadow: var(--shadow-md);
  color: var(--text-primary);
}

.popover-enter-active,
.popover-leave-active {
  transition:
    opacity var(--duration-fast) var(--ease-out),
    transform var(--duration-fast) var(--ease-out);
}

.popover-enter-from,
.popover-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}
</style>
