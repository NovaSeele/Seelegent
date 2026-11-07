<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import Button from '@/components/ui/Button.vue'
import Chip from '@/components/ui/Chip.vue'
import Icon from '@/components/ui/Icon.vue'

import type { RiskGroup } from './safety'

/**
 * Work at risk, per turn: the files whose uncommitted work the turn lost and why, with Restore (puts the files
 * not restored yet back from the snapshot taken before the turn) and the turn's changes outside the edit tools.
 * Once every file is back, the part reads as done: "Work restored", in the past tense.
 */
const props = defineProps<{
  groups: readonly RiskGroup[]
  /** The turn being restored. */
  busy: number | null
  /** Restore waits until the worker has stopped. */
  active: boolean
}>()
const emit = defineEmits<{ restore: [turn: number]; outside: [turn: number] }>()

const { t } = useI18n()

const done = computed(() => props.groups.every((g) => !g.open))
const title = computed(() =>
  done.value ? t('task.safety.risk.restoredTitle') : t('common.attention.workAtRisk'),
)
const desc = computed(() => {
  if (!done.value) return t('task.safety.risk.desc')
  const only = props.groups.length === 1 ? props.groups[0] : undefined
  return only
    ? t('task.safety.risk.restoredDesc', { n: only.turn })
    : t('task.safety.risk.restoredDescMany')
})
</script>

<template>
  <section class="risk" :aria-label="title">
    <h3 class="risk__title" :class="{ 'risk__title--done': done }">
      <Icon v-if="done" name="check" class="risk__check" />
      {{ title }}
    </h3>
    <p class="risk__desc">{{ desc }}</p>
    <div v-for="group in groups" :key="group.turn" class="risk__group">
      <div class="risk__head">
        <strong>{{ t('task.safety.turn', { n: group.turn }) }}</strong>
        <span class="risk__actions">
          <Button
            v-if="group.outsideDiff"
            size="sm"
            variant="ghost"
            @click="emit('outside', group.turn)"
          >
            {{ t('git.outside.open') }}
          </Button>
          <Button
            v-if="group.open"
            size="sm"
            variant="primary"
            :disabled="active"
            :loading="busy === group.turn"
            @click="emit('restore', group.turn)"
          >
            {{ t('task.safety.risk.restore', { n: group.open }) }}
          </Button>
        </span>
      </div>
      <p v-if="group.open && active" class="risk__desc">{{ t('task.safety.risk.waitStop') }}</p>
      <ul class="risk__files">
        <li v-for="file in group.files" :key="file.path" class="risk__file">
          <code class="risk__path">{{ file.path }}</code>
          <span class="risk__why">
            {{ t(`task.safety.risk.why.${file.why === 'large_removal' ? 'large' : 'outside'}`) }}
            <template v-if="file.deleted"> · {{ t('task.safety.risk.deleted') }}</template>
            <template v-else-if="file.binary"> · {{ t('task.safety.risk.binary') }}</template>
            <template v-else-if="file.lostLines">
              · {{ t('task.safety.risk.lost', { n: file.lostLines }) }}
            </template>
          </span>
          <Chip v-if="file.restored" tone="success" icon="check">
            {{ t('task.safety.risk.restored') }}
          </Chip>
        </li>
      </ul>
    </div>
  </section>
</template>

<style scoped>
.risk {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.risk__title {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
  font-weight: var(--fw-semibold);
}

.risk__title--done {
  color: var(--text-primary);
}

.risk__check {
  color: var(--status-accepted-fg);
  font-size: var(--icon-sm);
}

.risk__desc {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.risk__group {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.risk__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  font-size: var(--fs-sm);
}

.risk__actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.risk__files {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin: 0;
  padding: 0;
  list-style: none;
}

.risk__file {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) var(--space-2);
  font-size: var(--fs-sm);
}

.risk__path {
  min-width: 0;
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  overflow-wrap: anywhere;
}

.risk__why {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}
</style>
