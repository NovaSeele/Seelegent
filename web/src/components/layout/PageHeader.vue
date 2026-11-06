<script setup lang="ts">
/** Page title row: the h1, an optional one-line description, and actions on the right. */
defineProps<{ title: string; description?: string }>()
</script>

<template>
  <header class="page-header">
    <div class="page-header__text">
      <h1 class="page-header__title">{{ title }}</h1>
      <p v-if="description" class="page-header__desc">{{ description }}</p>
    </div>
    <div v-if="$slots.actions" class="page-header__actions"><slot name="actions" /></div>
  </header>
</template>

<style scoped>
.page-header {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  gap: var(--space-3);
  padding: var(--space-5) var(--space-6) var(--space-4);
}

.page-header__text {
  flex: 1 1 320px;
  min-width: 0;
}

.page-header__title {
  font-size: var(--fs-title);
  font-weight: var(--fw-semibold);
  line-height: var(--lh-tight);
  color: var(--text-primary);
  text-wrap: balance;
}

.page-header__desc {
  max-width: 72ch;
  margin-top: var(--space-1);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  line-height: var(--lh-prose);
  text-wrap: pretty;
}

.page-header__actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

@media (width < 600px) {
  .page-header {
    padding: var(--space-4) var(--space-4) var(--space-3);
  }

  .page-header__title {
    font-size: var(--fs-xl);
  }
}
</style>
