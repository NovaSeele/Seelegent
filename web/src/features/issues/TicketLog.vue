<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { TicketLogEntry } from '@/api/types'
import { useFormat } from '@/composables/useFormat'

import { logLine } from './ticketText'

/**
 * A ticket's log, newest first, folded: each step by its translated name, its data in one line with times and
 * durations formatted (the daemon has already masked tokens in it). Plain text only.
 */
const props = defineProps<{ entries: readonly TicketLogEntry[] }>()

const { t, te } = useI18n()
const fmt = useFormat()

const lines = computed(() => {
  const f = {
    duration: (ms: number) => fmt.value.duration(ms),
    dateTime: (iso: string) => fmt.value.dateTime(iso),
    word: (key: string) => (te(key) ? t(key) : null),
  }
  return [...props.entries].reverse().map((e) => ({ id: e.id, at: e.at, ...logLine(e, f) }))
})
</script>

<template>
  <section v-if="lines.length" class="sec" :aria-label="t('issues.ticket.log')">
    <details>
      <summary class="sec__summary">{{ t('issues.ticket.log') }} ({{ lines.length }})</summary>
      <ol class="list">
        <li v-for="e in lines" :key="e.id" class="log">
          <time class="log__at" :datetime="e.at">{{ fmt.dateTime(e.at) }}</time>
          <span class="log__type">{{ e.label }}</span>
          <span class="log__text">{{ e.text }}</span>
        </li>
      </ol>
    </details>
  </section>
</template>

<style scoped>
.sec {
  padding: var(--space-4);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
}

.sec__summary {
  border-radius: var(--radius-sm);
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
  cursor: pointer;
}

.sec__summary:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}

.list {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin: var(--space-2) 0 0;
  padding: 0;
  list-style: none;
}

.log {
  display: grid;
  grid-template-columns: auto auto 1fr;
  gap: var(--space-2);
  font-size: var(--fs-xs);
}

.log__at {
  color: var(--text-muted);
  white-space: nowrap;
}

.log__type {
  font-weight: var(--fw-medium);
  white-space: nowrap;
}

.log__text {
  min-width: 0;
  color: var(--text-secondary);
  overflow-wrap: anywhere;
}

@media (width < 600px) {
  .log {
    grid-template-columns: 1fr;
    gap: 0;
  }
}

@media (pointer: coarse) {
  .sec__summary {
    display: flex;
    align-items: center;
    min-height: var(--target-min);
  }
}
</style>
