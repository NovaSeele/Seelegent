<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import Button from '@/components/ui/Button.vue'
import Icon from '@/components/ui/Icon.vue'
import { useFormat } from '@/composables/useFormat'
import { distinctMB, freeMB, mbFormatter } from '@/lib/memory'
import { useConnectionStore } from '@/stores/connection'
import { useTasksStore } from '@/stores/tasks'

/**
 * Shown while the daemon's memory watchdog holds every new start (Health.lowMemory, kept current by the
 * resources.changed frame). That happens only below the emergency floor (criticalFreeMemoryMB), where gate
 * commands are stopped too, hence the danger tone. It says why: the free memory of that reading, the floor it is
 * below (by the setting's name) and how many tasks wait for memory, and links to the Resources settings.
 * Hidden while the dashboard is offline (the connection banner says that; the reading would be old).
 */
const { t } = useI18n()
const fmt = useFormat()
const connection = useConnectionStore()
const tasks = useTasksStore()

const shown = computed(() => connection.status !== 'offline' && !!connection.health?.lowMemory)

const facts = computed(() => {
  const memory = connection.health?.memory
  const phys = freeMB(memory, 'phys')
  const commit = freeMB(memory, 'commit')
  const floor = connection.settings?.criticalFreeMemoryMB ?? 0
  // The amounts read together: free memory just under the floor must not read the same as the floor.
  const shownMB = [phys, commit, floor > 0 ? floor : null]
  const present = shownMB.filter((mb): mb is number => mb !== null)
  const texts = distinctMB(mbFormatter(fmt.value), present)
  const [freeText, commitText, floorText] = shownMB.map((mb) =>
    mb === null ? null : (texts[present.indexOf(mb)] ?? ''),
  )
  const parts: string[] = []
  if (freeText) parts.push(t('nav.lowMemory.freePhys', { free: freeText }))
  if (commitText) parts.push(t('nav.lowMemory.freeCommit', { free: commitText }))
  if (floorText) {
    parts.push(
      t('nav.lowMemory.floor', {
        floor: floorText,
        setting: t('settings.items.criticalFreeMemory.label'),
      }),
    )
  }
  return parts.join(' · ')
})

/** Tasks held for memory (only computed while the banner shows). */
const waiting = computed(() =>
  shown.value
    ? tasks.all().filter((task) => task.status === 'waiting_quota' && task.waiting === 'memory')
        .length
    : 0,
)
</script>

<template>
  <div v-if="shown" class="banner tone-danger" role="status">
    <Icon name="gauge" class="banner__icon" />
    <p class="banner__text">
      <strong class="banner__title">{{ t('nav.lowMemory.low') }}</strong>
      <span v-if="facts" class="banner__free">{{ facts }}</span>
      <span v-if="waiting" class="banner__free" data-waiting>
        {{ t('nav.lowMemory.waiting', { count: waiting }, waiting) }}
      </span>
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
