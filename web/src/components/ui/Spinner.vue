<script setup lang="ts">
import { useI18n } from 'vue-i18n'

withDefaults(
  defineProps<{
    size?: 'sm' | 'md' | 'lg'
    /** Text read by screen readers; defaults to "Loading…". */
    label?: string
    /** Inside a control that already announces its busy state. */
    decorative?: boolean
  }>(),
  { size: 'md', label: undefined },
)

const { t } = useI18n()
</script>

<template>
  <span
    class="spinner"
    :class="`spinner--${size}`"
    :role="decorative ? undefined : 'status'"
    :aria-hidden="decorative ? 'true' : undefined"
  >
    <svg viewBox="0 0 24 24" fill="none" aria-hidden="true" focusable="false">
      <circle class="spinner__track" cx="12" cy="12" r="9" stroke-width="2.5" />
      <path class="spinner__arc" d="M12 3a9 9 0 0 1 9 9" stroke-width="2.5" />
    </svg>
    <span v-if="!decorative" class="sr-only">{{ label ?? t('common.ui.loading') }}</span>
  </span>
</template>

<style scoped>
.spinner {
  display: inline-flex;
  color: var(--status-running-fg);
}

.spinner svg {
  width: var(--spinner-size, var(--icon-md));
  height: var(--spinner-size, var(--icon-md));
  animation: spin 0.8s linear infinite;
}

:root[data-motion='off'] .spinner svg {
  animation: spin 2.4s linear infinite !important;
}

.spinner--sm {
  --spinner-size: var(--icon-sm);
}

.spinner--lg {
  --spinner-size: 28px;
}

.spinner__track {
  stroke: currentColor;
  opacity: 0.25;
}

.spinner__arc {
  stroke: currentColor;
  stroke-linecap: round;
}
</style>
