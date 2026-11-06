<script setup lang="ts">
import PlaceholderPage from '@/components/layout/PlaceholderPage.vue'

// The list is a readable-width page; the detail parameter (props: true, so it follows the page on screen even
// under the settings overlay) is accepted now and used when the detail view lands.
defineProps<{ contestId?: string }>()
</script>

<template>
  <PlaceholderPage id="contests" />
</template>
