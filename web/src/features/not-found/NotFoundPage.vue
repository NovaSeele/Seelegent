<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import Button from '@/components/ui/Button.vue'
import EmptyState from '@/components/ui/EmptyState.vue'

/** Unknown address: says so and offers the way home. */
const { t } = useI18n()
</script>

<template>
  <div class="not-found">
    <EmptyState
      icon="compass"
      :title="t('pages.notFound.title')"
      :description="t('pages.notFound.desc')"
    >
      <Button variant="primary" icon="watch" to="/">{{ t('pages.notFound.home') }}</Button>
    </EmptyState>
  </div>
</template>

<style scoped>
.not-found {
  display: flex;
  align-items: center;
  min-height: 100%;
}
</style>
