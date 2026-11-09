<script setup lang="ts">
import ServicesSection from '@/features/accounts/ServicesSection.vue'

import SettingRow from './SettingRow.vue'

/** The services API keys use, the same list as on the Accounts page (open here), searchable. */
defineProps<{ highlight?: string }>()
</script>

<template>
  <SettingRow item="providers" stacked :highlighted="highlight === 'providers'">
    <div class="providers-section">
      <ServicesSection :heading="false" :collapsible="false" />
    </div>
  </SettingRow>
</template>

<style scoped>
.providers-section {
  margin-top: var(--space-2);
}
</style>
