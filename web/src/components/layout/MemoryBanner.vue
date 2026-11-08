<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import Button from '@/components/ui/Button.vue'
import Icon from '@/components/ui/Icon.vue'
import { useFormat } from '@/composables/useFormat'
import { formatMB, freeMB } from '@/lib/memory'
import { useConnectionStore } from '@/stores/connection'

/**
 * Shown while the daemon's memory watchdog holds every new start (Health.lowMemory, kept current by the
 * resources.changed frame): says so with the free memory of that reading and links to the Resources settings.
 * Hidden while the dashboard is offline (the connection banner says that; the reading would be old).
 */
const { t } = useI18n()
const fmt = useFormat()
const connection = useConnectionStore()

const shown = computed(() => connection.status !== 'offline' && !!connection.health?.lowMemory)

const free = computed(() => {
  const memory = connection.health?.memory
  const phys = freeMB(memory, 'phys')
  const commit = freeMB(memory, 'commit')
  const parts: string[] = []
  if (phys !== null) parts.push(t('nav.lowMemory.freePhys', { free: formatMB(fmt.value, phys) }))
  if (commit !== null)
    parts.push(t('nav.lowMemory.freeCommit', { free: formatMB(fmt.value, commit) }))
  return parts.join(' · ')
})
</script>

<template>
  <div v-if="shown" class="banner tone-danger" role="status">
    <Icon name="gauge" class="banner__icon" />
    <p class="banner__text">
      <strong class="banner__title">{{ t('nav.lowMemory.low') }}</strong>
      <span v-if="free" class="banner__free">{{ free }}</span>
    </p>
    <Button size="sm" variant="ghost" :to="{ name: 'settings', params: { section: 'resources' } }">
      {{ t('nav.lowMemory.settings') }}
    </Button>
  </div>
</template>

<style scoped>
.banner {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: var(--control-lg);
  padding: var(--space-1) var(--space-1) var(--space-1) var(--space-3);
  border: var(--border-width) solid var(--tone-border);
  border-radius: var(--radius-lg);
  background: var(--tone-bg);
  color: var(--text-primary);
  font-size: var(--fs-sm);
}

.banner__icon {
  flex: none;
  color: var(--tone-fg);
  font-size: var(--icon-md);
}

.banner__text {
  display: flex;
  flex: 1;
  flex-wrap: wrap;
  gap: 0 var(--space-2);
  min-width: 0;
}

.banner__title {
  font-weight: var(--fw-medium);
}

.banner__free {
  color: var(--text-secondary);
  font-variant-numeric: tabular-nums;
}

@media (width < 600px) {
  .banner {
    border-width: 0 0 var(--border-width);
    border-radius: 0;
  }
}
</style>
