<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type { ProofFeature } from '@/api/types'

import ProofCounts from './ProofCounts.vue'

/** The Spec Kit features of the repository; picking one shows its matrix. A list of toggle buttons. */
defineProps<{ features: readonly ProofFeature[]; selected: string }>()
const emit = defineEmits<{ select: [dir: string] }>()

const { t } = useI18n()
</script>

<template>
  <section class="features" :aria-label="t('proof.features.title')">
    <h2 class="features__title">{{ t('proof.features.title') }}</h2>
    <p v-if="!features.length" class="features__empty">{{ t('proof.features.empty') }}</p>
    <ul v-else class="features__list">
      <li v-for="f in features" :key="f.dir">
        <button
          type="button"
          class="features__item"
          :class="{ 'features__item--on': f.dir === selected }"
          :aria-pressed="f.dir === selected"
          @click="emit('select', f.dir)"
        >
          <span class="features__head">
            <span class="features__name">{{ f.title || f.dir }}</span>
            <span class="features__tasks">
              {{ t('proof.features.tasks', { done: f.tasks.done, total: f.tasks.total }) }}
            </span>
          </span>
          <code class="features__dir">{{ f.dir }}</code>
          <span v-if="f.error" class="features__error">{{ f.error }}</span>
          <ProofCounts v-else :counts="f.counts" compact />
        </button>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.features {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}

.features__title {
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
}

.features__empty {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.features__list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.features__item {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  width: 100%;
  min-height: var(--target-min);
  padding: var(--space-2) var(--space-3);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
  color: var(--text-primary);
  text-align: left;
  cursor: pointer;
}

.features__item:hover {
  border-color: var(--border-strong);
}

.features__item:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}

.features__item--on {
  border-color: var(--accent);
  box-shadow: inset 3px 0 0 var(--accent);
}

.features__head {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: var(--space-1) var(--space-2);
}

.features__name {
  min-width: 0;
  font-size: var(--fs-sm);
  font-weight: var(--fw-medium);
  overflow-wrap: anywhere;
}

.features__tasks {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.features__dir {
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  overflow-wrap: anywhere;
}

.features__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-xs);
}
</style>
