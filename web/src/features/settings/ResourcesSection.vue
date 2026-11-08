<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'

import { useFormat } from '@/composables/useFormat'
import { formatMB } from '@/lib/memory'
import { useConnectionStore } from '@/stores/connection'

import DaemonSection from './DaemonSection.vue'

/**
 * Resources (docs/configuration.md "Resources"): the daemon settings of the section, the machine the auto values
 * are computed from (Health.machine), and a fresh reading of the machine's free memory while it is open, so the
 * thresholds sit next to what is free now. Health is read when the section opens and every READ_EVERY_MS while
 * the tab is visible; nothing runs once it closes. After a settings change the connection store reads it again
 * (the values in use follow the settings), open or not.
 */
defineProps<{ highlight?: string }>()

const READ_EVERY_MS = 10_000

const { t } = useI18n()
const fmt = useFormat()
const connection = useConnectionStore()
let timer: ReturnType<typeof setInterval> | undefined
const controller = new AbortController()

function read() {
  if (document.visibilityState === 'hidden') return
  void connection.refreshHealth(controller.signal)
}

/** "This machine: 31.9 GB RAM, 24 CPUs" (with the commit limit where it is known), or '' without a reading. */
const machineText = computed(() => {
  const machine = connection.health?.machine
  if (!machine) return ''
  const cpus = t(
    'settings.daemon.machine.cpus',
    { n: fmt.value.number(machine.cpus) },
    machine.cpus,
  )
  if (machine.totalPhysMB <= 0) return t('settings.daemon.machine.cpusOnly', { cpus })
  const memory = formatMB(fmt.value, machine.totalPhysMB)
  return machine.totalCommitMB > 0
    ? t('settings.daemon.machine.withCommit', {
        memory,
        commit: formatMB(fmt.value, machine.totalCommitMB),
        cpus,
      })
    : t('settings.daemon.machine.full', { memory, cpus })
})

onMounted(() => {
  read()
  timer = setInterval(read, READ_EVERY_MS)
})
onBeforeUnmount(() => {
  clearInterval(timer)
  controller.abort()
})
</script>

<template>
  <div>
    <p v-if="machineText && connection.settings" class="resources__machine" data-machine>
      {{ machineText }}
    </p>
    <DaemonSection section="resources" :highlight="highlight" />
  </div>
</template>

<style scoped>
.resources__machine {
  margin-top: var(--space-2);
  padding-bottom: var(--space-3);
  border-bottom: var(--border-width) solid var(--border-subtle);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  font-variant-numeric: tabular-nums;
}
</style>
