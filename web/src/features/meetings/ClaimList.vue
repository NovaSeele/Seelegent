<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type { Claim } from '@/api/types'

import { citeText, citeTone } from './meetingModel'

/**
 * Claims with their citations. Each citation shows its path:line and how Seelegent's check found it (verified,
 * located, quote differs…), in words as well as colour.
 */
defineProps<{ claims: readonly Claim[] }>()

const { t } = useI18n()
</script>

<template>
  <ul class="claims">
    <li v-for="(claim, i) in claims" :key="i" class="claim">
      <p class="claim__text">{{ claim.text }}</p>
      <ul v-if="claim.cites.length" class="claim__cites">
        <li
          v-for="(cite, j) in claim.cites"
          :key="j"
          class="cite"
          :class="`tone-${citeTone(cite.state)}`"
          :title="cite.detail || cite.quote || undefined"
        >
          <code>{{ cite.variant ? `${cite.variant} ` : '' }}{{ citeText(cite) }}</code>
          <span class="cite__state">{{ t(`team.meetings.cite.${cite.state}`) }}</span>
        </li>
      </ul>
    </li>
  </ul>
</template>

<style scoped>
.claims {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.claim__text {
  overflow-wrap: anywhere;
}

.claim__cites {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  margin: var(--space-1) 0 0;
  padding: 0;
  list-style: none;
}

.cite {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  max-width: 100%;
  padding: 0 var(--space-2);
  border: var(--border-width) solid var(--tone-border);
  border-radius: var(--radius-pill);
  background: var(--tone-bg);
  color: var(--tone-fg);
  font-size: var(--fs-2xs);
}

.cite code {
  overflow-wrap: anywhere;
  font-family: var(--font-mono);
}

.cite__state {
  white-space: nowrap;
}
</style>
