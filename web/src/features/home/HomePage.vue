<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const { t } = useI18n()
</script>

<template>
  <main class="home">
    <h1 class="home__title">{{ t('app.title') }}</h1>
  </main>
</template>

<style scoped>
.home {
  padding: var(--space-6) var(--space-4);
}

.home__title {
  margin: 0;
  color: var(--color-text);
}
</style>
