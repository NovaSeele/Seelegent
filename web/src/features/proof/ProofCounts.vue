<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ProofCounts } from '@/api/types'

import { countParts, statusTone, total } from './proofModel'

/**
 * A requirement count as a stacked bar (proportions) with the numbers spelled out next to it, so the status is
 * never told by colour alone.
 */
const props = defineProps<{ counts: ProofCounts; compact?: boolean }>()

const { t } = useI18n()

const parts = computed(() => countParts(props.counts))
const sum = computed(() => total(props.counts))
</script>

<template>
  <div class="counts" :class="{ 'counts--compact': compact }">
    <div v-if="sum" class="counts__bar" aria-hidden="true">
      <span
        v-for="p in parts"
        :key="p.status"
        class="counts__seg"
        :class="`tone-${statusTone(p.status)}`"
        :style="{ flexGrow: p.n }"
      />
    </div>
    <ul class="counts__list">
      <li v-if="!sum" class="counts__item counts__item--none">{{ t('proof.counts.none') }}</li>
      <li
        v-for="p in parts"
        :key="p.status"
        class="counts__item"
        :class="`tone-${statusTone(p.status)}`"
      >
        <span class="counts__dot" aria-hidden="true" />
        {{ t(`proof.status.${p.status}`) }} {{ p.n }}
      </li>
    </ul>
  </div>
</template>

<style scoped>
.counts {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.counts__bar {
  display: flex;
  gap: 2px;
  height: 6px;
  overflow: hidden;
  border-radius: var(--radius-pill);
}

.counts--compact .counts__bar {
  height: 4px;
}

.counts__seg {
  min-width: 4px;
  background: var(--tone-fg);
}

.counts__list {
  display: flex;
  flex-wrap: wrap;
  gap: 0 var(--space-3);
  margin: 0;
  padding: 0;
  list-style: none;
  font-size: var(--fs-xs);
  color: var(--text-secondary);
}

.counts__item {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
}

.counts__dot {
  width: 8px;
  height: 8px;
  border-radius: var(--radius-pill);
  background: var(--tone-fg);
}
</style>
