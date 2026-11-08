<script setup lang="ts">
import { useI18n } from 'vue-i18n'

/**
 * The daemon's own message (English, often git's or GitHub's words) behind a labelled disclosure: the view says
 * what happened in the reader's language, and this keeps the original for whoever needs it (F14).
 */
defineProps<{ text: string }>()

const { t } = useI18n()
</script>

<template>
  <details class="said">
    <summary class="said__summary">{{ t('common.daemonSaid') }}</summary>
    <code class="said__text">{{ text }}</code>
  </details>
</template>

<style scoped>
.said {
  min-width: 0;
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.said__summary {
  width: fit-content;
  border-radius: var(--radius-sm);
  cursor: pointer;
}

.said__summary:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}

.said__text {
  display: block;
  margin-top: var(--space-1);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

@media (pointer: coarse) {
  .said__summary {
    display: flex;
    align-items: center;
    min-height: var(--target-min);
  }
}
</style>
