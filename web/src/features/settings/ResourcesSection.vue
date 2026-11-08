<script setup lang="ts">
import { onBeforeUnmount, onMounted } from 'vue'

import { useConnectionStore } from '@/stores/connection'

import DaemonSection from './DaemonSection.vue'

/**
 * Resources (docs/configuration.md "Resources"): the daemon settings of the section, and a fresh reading of the
 * machine's free memory while it is open, so the thresholds sit next to what is free now. The reading is taken
 * when the section opens and every READ_EVERY_MS while the tab is visible; nothing runs once it closes.
 */
defineProps<{ highlight?: string }>()

const READ_EVERY_MS = 10_000

const connection = useConnectionStore()
let timer: ReturnType<typeof setInterval> | undefined
const controller = new AbortController()

function read() {
  if (document.visibilityState === 'hidden') return
  void connection.refreshHealth(controller.signal)
}

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
  <DaemonSection section="resources" :highlight="highlight" />
</template>
