<script setup lang="ts">
import { computed, ref, toRef, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import { useOverlay } from '@/composables/useOverlay'

import IconButton from './IconButton.vue'

/**
 * Modal dialog: teleported to <body>, focus trapped while on top of the layer stack, Esc / backdrop / close
 * button close it, focus returns to the opener. On phones, `lg` and `xl` dialogs fill the screen.
 * `bare` drops the built-in header and padding for custom layouts; give it `ariaLabel` then.
 */
const props = withDefaults(
  defineProps<{
    open: boolean
    title?: string
    description?: string
    size?: 'sm' | 'md' | 'lg' | 'xl'
    bare?: boolean
    ariaLabel?: string
    /** Backdrop click and Esc close the dialog (the close button always does). */
    dismissible?: boolean
    /** Selector inside the dialog to focus first. */
    initialFocus?: string
  }>(),
  {
    title: undefined,
    description: undefined,
    size: 'md',
    ariaLabel: undefined,
    dismissible: true,
    initialFocus: undefined,
  },
)

const emit = defineEmits<{ 'update:open': [value: boolean]; close: [] }>()

const { t } = useI18n()
const id = useId()
const panel = ref<HTMLElement | null>(null)

function close() {
  emit('update:open', false)
  emit('close')
}

useOverlay(toRef(props, 'open'), panel, {
  kind: 'dialog',
  onClose: () => {
    if (props.dismissible) close()
  },
  initialFocus: () =>
    props.initialFocus ? panel.value?.querySelector<HTMLElement>(props.initialFocus) : null,
})

const labelledBy = computed(() => (!props.bare && props.title ? `${id}-title` : undefined))

function onBackdrop(event: MouseEvent) {
  if (event.target === event.currentTarget && props.dismissible) close()
}

defineExpose({ close })
</script>

<template>
  <Teleport to="body">
    <Transition name="dialog">
      <div v-if="open" class="dialog-backdrop" @mousedown="onBackdrop">
        <section
          ref="panel"
          class="dialog"
          :class="[`dialog--${size}`, { 'dialog--bare': bare }]"
          role="dialog"
          aria-modal="true"
          :aria-labelledby="labelledBy"
          :aria-label="labelledBy ? undefined : (ariaLabel ?? title)"
          :aria-describedby="description ? `${id}-desc` : undefined"
          tabindex="-1"
        >
          <header v-if="!bare" class="dialog__header">
            <div class="dialog__heading">
              <h2 :id="`${id}-title`" class="dialog__title">{{ title }}</h2>
              <p v-if="description" :id="`${id}-desc`" class="dialog__desc">{{ description }}</p>
            </div>
            <slot name="header-actions" />
            <IconButton
              icon="x"
              :label="t('common.action.close')"
              :tooltip="false"
              @click="close"
            />
          </header>
          <div class="dialog__body"><slot :close="close" /></div>
          <footer v-if="$slots.footer && !bare" class="dialog__footer">
            <slot name="footer" :close="close" />
          </footer>
        </section>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.dialog-backdrop {
  position: fixed;
  inset: 0;
  z-index: var(--z-overlay);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-6) var(--space-4);
  background: var(--bg-backdrop);
}

.dialog {
  --dialog-width: 560px;
  display: flex;
  flex-direction: column;
  width: min(100%, var(--dialog-width));
  max-height: min(100%, 860px);
  overflow: hidden;
  border: var(--border-width) solid var(--border-strong);
  border-radius: var(--radius-xl);
  background: var(--bg-elevated);
  box-shadow: var(--shadow-lg);
  color: var(--text-primary);
}

.dialog:focus {
  outline: none;
}

.dialog--sm {
  --dialog-width: 400px;
}

.dialog--lg {
  --dialog-width: 880px;
}

.dialog--xl {
  --dialog-width: 1080px;
  height: min(100%, 760px);
}

.dialog__header {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  padding: var(--space-4) var(--space-3) var(--space-3) var(--space-5);
  border-bottom: var(--border-width) solid var(--border-subtle);
}

.dialog__heading {
  flex: 1;
  min-width: 0;
  padding-top: var(--space-1);
}

.dialog__title {
  font-size: var(--fs-lg);
  font-weight: var(--fw-semibold);
  line-height: var(--lh-tight);
}

.dialog__desc {
  margin-top: var(--space-1);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.dialog__body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: var(--space-5);
}

.dialog--bare .dialog__body {
  display: flex;
  flex-direction: column;
  padding: 0;
  overflow: hidden;
}

.dialog__footer {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-5);
  border-top: var(--border-width) solid var(--border-subtle);
}

@media (width < 600px) {
  .dialog-backdrop {
    align-items: flex-end;
    padding: 0;
  }

  .dialog {
    width: 100%;
    max-height: calc(100dvh - var(--space-6));
    border-bottom-left-radius: 0;
    border-bottom-right-radius: 0;
    padding-bottom: env(safe-area-inset-bottom);
  }

  .dialog--lg,
  .dialog--xl {
    height: 100dvh;
    max-height: none;
    border: 0;
    border-radius: 0;
  }
}

.dialog-enter-active,
.dialog-leave-active {
  transition: opacity var(--duration-base) var(--ease-standard);
}

.dialog-enter-active .dialog,
.dialog-leave-active .dialog {
  transition: transform var(--duration-base) var(--ease-standard);
}

.dialog-enter-from,
.dialog-leave-to {
  opacity: 0;
}

.dialog-enter-from .dialog,
.dialog-leave-to .dialog {
  transform: translateY(8px) scale(0.98);
}
</style>
