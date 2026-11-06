<script setup lang="ts">
import PlaceholderPage from '@/components/layout/PlaceholderPage.vue'

// The route passes its parameter as a prop (props: true), so it follows the page on screen even while an
// overlay route (settings) is the current one.
defineProps<{ contestId?: string }>()
</script>

<template>
  <PlaceholderPage id="contests" split :detail-id="contestId" />
</template>
