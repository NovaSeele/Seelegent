<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { ContestVariant } from '@/api/types'
import StatusPill from '@/components/ui/StatusPill.vue'

import { variantRunsOn, variantStatus } from './contestModel'

/**
 * One row per variant: its label, what it runs on, its task's status and a link to the task; a removed worktree
 * or a cleanup that failed says so. The running contest's main view, before there is a scoreboard.
 */
const props = defineProps<{ variants: readonly ContestVariant[] }>()

const { t } = useI18n()
const rows = computed(() =>
  props.variants.map((v) => ({ v, s: variantStatus(v.taskId ? v.status : undefined) })),
)
</script>

<template>
  <ul class="variants">
    <li v-for="{ v, s } in rows" :key="v.label" class="variant">
      <strong class="variant__label">{{ v.label }}</strong>
      <span class="variant__runs">{{ variantRunsOn(v) }}</span>
      <StatusPill v-if="'status' in s" size="sm" :status="s.status" />
      <StatusPill v-else size="sm" :tone="s.tone" :label="t(s.key)" />
      <RouterLink
        v-if="v.taskId"
        class="variant__task"
        :to="{ name: 'tasks', params: { taskId: v.taskId } }"
        :aria-label="t('team.contests.openTask', { label: v.label })"
      >
        {{ v.taskId }}
      </RouterLink>
      <span v-if="v.removed" class="variant__muted">{{ t('team.contests.removed') }}</span>
      <span v-if="v.cleanupError" class="variant__error">
        {{ t('team.contests.cleanupError', { error: v.cleanupError }) }}
      </span>
    </li>
  </ul>
</template>

<style scoped>
.variants {
  display: flex;
  flex-direction: column;
  margin: 0;
  padding: 0;
  list-style: none;
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
  font-size: var(--fs-sm);
}

.variant {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) var(--space-3);
  min-width: 0;
  padding: var(--space-2) var(--space-4);
}

.variant + .variant {
  border-top: var(--border-width) solid var(--border-subtle);
}

.variant__runs,
.variant__muted {
  color: var(--text-secondary);
  overflow-wrap: anywhere;
}

.variant__task {
  margin-left: auto;
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
}

.variant__error {
  color: var(--status-danger-fg);
  overflow-wrap: anywhere;
}
</style>
