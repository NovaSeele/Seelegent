<script setup lang="ts">
import { computed, ref, toRef, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import { useBreakpoint } from '@/composables/useMediaQuery'
import { useOverlay } from '@/composables/useOverlay'

import IconButton from './IconButton.vue'

/**
 * A drawer that slides in from the right, or a bottom sheet. `side="auto"` is a bottom sheet on phones and a
 * right drawer elsewhere. Same modal behaviour as Dialog (layer stack, focus trap, Esc, focus return).
 */
const props = withDefaults(
  defineProps<{
    open: boolean
    title: string
    side?: 'right' | 'bottom' | 'auto'
    /** Visually hide the title (it still names the sheet). */
    hideTitle?: boolean
  }>(),
  { side: 'auto' },
)

const emit = defineEmits<{ 'update:open': [value: boolean]; close: [] }>()

const { t } = useI18n()
const id = useId()
const panel = ref<HTMLElement | null>(null)
const { isMobile } = useBreakpoint()

const resolvedSide = computed(() =>
  props.side === 'auto' ? (isMobile.value ? 'bottom' : 'right') : props.side,
)

function close() {
  emit('update:open', false)
  emit('close')
}

useOverlay(toRef(props, 'open'), panel, { kind: 'sheet', onClose: close })

function onBackdrop(event: MouseEvent) {
  if (event.target === event.currentTarget) close()
}
</script>

<template>
  <Teleport to="body">
    <Transition :name="`sheet-${resolvedSide}`">
      <div
        v-if="open"
        class="sheet-backdrop"
        :class="`sheet-backdrop--${resolvedSide}`"
        @mousedown="onBackdrop"
      >
        <section
          ref="panel"
          class="sheet"
          :class="`sheet--${resolvedSide}`"
          role="dialog"
          aria-modal="true"
          :aria-labelledby="`${id}-title`"
          tabindex="-1"
        >
          <span v-if="resolvedSide === 'bottom'" class="sheet__handle" aria-hidden="true" />
          <header class="sheet__header" :class="{ 'sheet__header--hidden': hideTitle }">
            <h2 :id="`${id}-title`" class="sheet__title" :class="{ 'sr-only': hideTitle }">
              {{ title }}
            </h2>
            <IconButton
              v-if="!hideTitle"
              icon="x"
              :label="t('common.action.close')"
              :tooltip="false"
              @click="close"
            />
          </header>
          <div class="sheet__body"><slot :close="close" /></div>
        </section>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.sheet-backdrop {
  position: fixed;
  inset: 0;
  z-index: var(--z-overlay);
  display: flex;
  background: var(--bg-backdrop);
}

.sheet-backdrop--right {
  justify-content: flex-end;
}

.sheet-backdrop--bottom {
  align-items: flex-end;
}

.sheet {
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border: var(--border-width) solid var(--border-strong);
  background: var(--bg-elevated);
  box-shadow: var(--shadow-lg);
  color: var(--text-primary);
}

.sheet:focus {
  outline: none;
}

.sheet--right {
  width: min(420px, 100%);
  height: 100%;
  border-width: 0 0 0 var(--border-width);
}

.sheet--bottom {
  width: 100%;
  max-height: 85dvh;
  border-width: var(--border-width) 0 0;
  border-radius: var(--radius-xl) var(--radius-xl) 0 0;
  padding-bottom: env(safe-area-inset-bottom);
}

.sheet__handle {
  align-self: center;
  width: 36px;
  height: 4px;
  margin-top: var(--space-2);
  border-radius: var(--radius-pill);
  background: var(--border-strong);
}

.sheet__header {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-3) var(--space-2) var(--space-4);
}

.sheet__header--hidden {
  padding: 0;
}

.sheet__title {
  flex: 1;
  min-width: 0;
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
}

.sheet__body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: var(--space-2) var(--space-4) var(--space-4);
}

.sheet-right-enter-active,
.sheet-right-leave-active,
.sheet-bottom-enter-active,
.sheet-bottom-leave-active {
  transition: opacity var(--duration-base) var(--ease-standard);
}

.sheet-right-enter-active .sheet,
.sheet-right-leave-active .sheet,
.sheet-bottom-enter-active .sheet,
.sheet-bottom-leave-active .sheet {
  transition: transform var(--duration-slow) var(--ease-standard);
}

.sheet-right-enter-from,
.sheet-right-leave-to,
.sheet-bottom-enter-from,
.sheet-bottom-leave-to {
  opacity: 0;
}

.sheet-right-enter-from .sheet,
.sheet-right-leave-to .sheet {
  transform: translateX(100%);
}

.sheet-bottom-enter-from .sheet,
.sheet-bottom-leave-to .sheet {
  transform: translateY(100%);
}
</style>
