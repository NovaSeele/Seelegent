<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import { useToastStore } from '@/stores/toast'

import ToastItem from './ToastItem.vue'

/**
 * Where toasts appear: bottom-right on wide screens, above the bottom navigation on phones. Focus never moves
 * into a toast by itself; each toast announces itself (role=status, or role=alert for errors).
 */
const store = useToastStore()
const { t } = useI18n()
</script>

<template>
  <Teleport to="body">
    <section class="toasts" :aria-label="t('common.toast.region')">
      <TransitionGroup name="toast">
        <ToastItem
          v-for="toast in store.toasts"
          :key="toast.id"
          :toast="toast"
          @dismiss="store.dismiss"
        />
      </TransitionGroup>
    </section>
  </Teleport>
</template>

<style scoped>
.toasts {
  position: fixed;
  right: var(--space-4);
  bottom: var(--space-4);
  z-index: var(--z-toast);
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: var(--space-2);
  width: min(380px, calc(100vw - 2 * var(--space-4)));
  pointer-events: none;
}

@media (width < 600px) {
  .toasts {
    right: var(--space-2);
    left: var(--space-2);
    bottom: calc(var(--bottom-nav-height) + env(safe-area-inset-bottom) + var(--space-2));
    width: auto;
  }
}

.toast-enter-active,
.toast-leave-active {
  transition:
    opacity var(--duration-base) var(--ease-standard),
    transform var(--duration-base) var(--ease-standard);
}

.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateY(8px);
}

.toast-move {
  transition: transform var(--duration-base) var(--ease-standard);
}
</style>
