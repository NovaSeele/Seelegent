<script setup lang="ts">
import Icon from './Icon.vue'
import type { IconName } from './icons'

withDefaults(
  defineProps<{
    icon?: IconName
    title: string
    description?: string
    /** Heading level of the title inside the page outline. */
    level?: 2 | 3
  }>(),
  { icon: undefined, description: undefined, level: 2 },
)
</script>

<template>
  <div class="empty">
    <span v-if="icon" class="empty__icon" aria-hidden="true">
      <Icon :name="icon" :stroke-width="1.6" />
    </span>
    <component :is="`h${level}`" class="empty__title">{{ title }}</component>
    <p v-if="description" class="empty__desc">{{ description }}</p>
    <div v-if="$slots.default" class="empty__actions"><slot /></div>
  </div>
</template>

<style scoped>
.empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-2);
  max-width: 440px;
  margin: 0 auto;
  padding: var(--space-8) var(--space-4);
  text-align: center;
}

.empty__icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 48px;
  height: 48px;
  margin-bottom: var(--space-2);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface-2);
  color: var(--text-muted);
  font-size: 22px;
}

.empty__title {
  font-size: var(--fs-lg);
  font-weight: var(--fw-semibold);
  line-height: var(--lh-tight);
  color: var(--text-primary);
  text-wrap: balance;
}

.empty__desc {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  line-height: var(--lh-prose);
  text-wrap: pretty;
}

.empty__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: var(--space-2);
  margin-top: var(--space-3);
}
</style>
