<script setup lang="ts">
import PlaceholderPage from '@/components/layout/PlaceholderPage.vue'

// The route passes its parameter as a prop (props: true), so it follows the page on screen even while an
// overlay route (settings) is the current one.
defineProps<{ taskId?: string }>()
</script>

<template>
  <PlaceholderPage id="tasks" split :detail-id="taskId" />
</template>
