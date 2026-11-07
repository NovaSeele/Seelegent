<script setup lang="ts">
import ProvidersPanel from '@/features/accounts/ProvidersPanel.vue'

import SettingRow from './SettingRow.vue'

/** API providers, the same list as on the Accounts page, searchable. */
defineProps<{ highlight?: string }>()
</script>

<template>
  <SettingRow item="providers" stacked :highlighted="highlight === 'providers'">
    <div class="providers-section">
      <ProvidersPanel :heading="false" />
    </div>
  </SettingRow>
</template>

<style scoped>
.providers-section {
  margin-top: var(--space-2);
}
</style>
