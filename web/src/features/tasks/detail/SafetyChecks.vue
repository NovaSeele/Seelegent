<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import Chip from '@/components/ui/Chip.vue'

import { findingKey, type Safety } from './safety'

/**
 * The safety panel's lists after work at risk: files other tasks of the repository changed at the same time, the
 * latest turn's hygiene and scope findings, and the commands the git guard refused.
 */
defineProps<{ safety: Safety }>()

const { t } = useI18n()
</script>

<template>
  <section v-if="safety.overlaps.length" class="checks">
    <h3 class="checks__title">{{ t('task.safety.overlaps.title') }}</h3>
    <ul class="checks__list">
      <li v-for="o in safety.overlaps" :key="`${o.taskId}:${o.file}`" class="checks__item">
        <code class="checks__path">{{ o.file }}</code>
        <RouterLink :to="{ name: 'tasks', params: { taskId: o.taskId } }" class="checks__link">
          {{ o.title || o.taskId }}
        </RouterLink>
        <Chip :tone="o.severity === 'active' ? 'danger' : 'neutral'">
          {{ t(`task.safety.overlaps.${o.severity}`) }}
        </Chip>
      </li>
    </ul>
  </section>
  <section v-if="safety.findings.length" class="checks">
    <h3 class="checks__title">
      {{ t('task.safety.findings', { n: safety.findingsTurn ?? 0 }) }}
    </h3>
    <ul class="checks__list">
      <li v-for="(f, i) in safety.findings" :key="i" class="checks__item">
        <Chip :tone="f.severity === 'red' ? 'danger' : 'attention'">
          {{ t(`task.safety.severity.${f.severity}`) }}
        </Chip>
        <span>{{ t(findingKey(f.kind)) }}</span>
        <code v-if="f.path" class="checks__path">{{ f.path }}</code>
        <span v-if="f.detail" class="checks__muted">{{ f.detail }}</span>
      </li>
    </ul>
  </section>
  <section v-if="safety.blocked.length" class="checks">
    <h3 class="checks__title">{{ t('task.safety.blocked') }}</h3>
    <ul class="checks__list">
      <li v-for="(b, i) in safety.blocked" :key="i" class="checks__item">
        <span class="checks__muted">{{ t('task.safety.turn', { n: b.turn }) }}</span>
        <code class="checks__path">{{ b.command }}</code>
      </li>
    </ul>
    <p class="checks__muted">{{ t('task.safety.blockedNote') }}</p>
  </section>
</template>

<style scoped>
.checks {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.checks__title {
  font-size: var(--fs-sm);
  font-weight: var(--fw-semibold);
}

.checks__list {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin: 0;
  padding: 0;
  list-style: none;
}

.checks__item {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) var(--space-2);
  font-size: var(--fs-sm);
}

.checks__path {
  min-width: 0;
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  overflow-wrap: anywhere;
}

.checks__link {
  color: var(--accent);
}

.checks__muted {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  overflow-wrap: anywhere;
}

/* A touch target of at least 44px around the link, without changing how it looks. */
@media (pointer: coarse) {
  .checks__link {
    position: relative;
  }

  .checks__link::after {
    position: absolute;
    inset: 50% 0 auto;
    min-width: var(--target-min);
    height: var(--target-min);
    content: '';
    transform: translateY(-50%);
  }
}
</style>
