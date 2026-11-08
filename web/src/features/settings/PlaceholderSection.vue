<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import EmptyState from '@/components/ui/EmptyState.vue'

import { sectionInfo, type SectionId } from './sections'

/**
 * A section whose settings live in the daemon and arrive with its API: says so and lists what the section will
 * hold (the same entries settings search finds).
 */
const props = defineProps<{ section: SectionId; highlight?: string }>()

const { t } = useI18n()
const info = sectionInfo(props.section)
</script>

<template>
  <div>
    <EmptyState
      :icon="info.icon"
      :title="t('settings.comingSoon.title')"
      :description="t('settings.comingSoon.desc')"
      :level="3"
    />
    <ul class="planned" :aria-label="t('settings.comingSoon.planned')">
      <li
        v-for="item in info.items"
        :key="item"
        class="planned__item"
        :class="{ 'planned__item--highlighted': highlight === item }"
        :data-setting="item"
      >
        <span class="planned__label">{{ t(`settings.items.${item}.label`) }}</span>
        <span class="planned__desc">{{ t(`settings.items.${item}.desc`) }}</span>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.planned {
  display: flex;
  flex-direction: column;
  max-width: 560px;
  margin: 0 auto;
  padding: 0;
  list-style: none;
  border: var(--border-width) dashed var(--border-strong);
  border-radius: var(--radius-lg);
}

.planned__item {
  display: flex;
  flex-direction: column;
  gap: var(--space-0);
  padding: var(--space-3) var(--space-4);
  scroll-margin: var(--space-4);
}

.planned__item + .planned__item {
  border-top: var(--border-width) dashed var(--border-subtle);
}

.planned__item--highlighted {
  background: var(--accent-soft);
  --text-muted: var(--text-primary);
  --text-secondary: var(--text-primary);
}

.planned__label {
  color: var(--text-primary);
  font-weight: var(--fw-medium);
}

.planned__desc {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}
</style>
