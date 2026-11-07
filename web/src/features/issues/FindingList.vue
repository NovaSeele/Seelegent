<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import { truncateMiddle } from '@/lib/paths'

import type { FindingView, Words } from './ticketText'

/** Refused files, each with what it is and the step that fixes it. Paths are plain text, middle-truncated. */
defineProps<{ findings: readonly FindingView[] }>()

const { t } = useI18n()
const say = (w: Words) => t(w.key, w.params, w.n ?? 1)
</script>

<template>
  <ul class="findings">
    <li v-for="f in findings" :key="f.path" class="finding">
      <code class="finding__path" :title="f.path">{{ truncateMiddle(f.path, 60) }}</code>
      <span class="finding__what">{{ say(f.what) }}</span>
      <span class="finding__fix">{{ say(f.fix) }}</span>
    </li>
  </ul>
</template>

<style scoped>
.findings {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.finding {
  display: grid;
  grid-template-columns: minmax(0, max-content) 1fr;
  gap: 0 var(--space-3);
  font-size: var(--fs-sm);
}

.finding__path {
  grid-row: span 2;
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
  overflow-wrap: anywhere;
}

.finding__what {
  color: var(--text-primary);
}

.finding__fix {
  color: var(--text-secondary);
}

@media (width < 600px) {
  .finding {
    grid-template-columns: 1fr;
  }

  .finding__path {
    grid-row: auto;
  }
}
</style>
