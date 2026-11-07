<script setup lang="ts">
import { computed, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Automation } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import StatusPill from '@/components/ui/StatusPill.vue'
import { useFormat } from '@/composables/useFormat'

import { automationState, OUTCOME_LOOK, STATE_LOOK } from './automationModel'
import { useScheduleText } from './useScheduleText'

/**
 * One automation: its name, schedule and folder, whether it is on (or waits for the user's confirmation), the
 * next and the last run, and its actions. The page owns the dialogs; the card only asks for them. An automation
 * whose content the user has not confirmed cannot run (the daemon refuses run now): its card offers "Review and
 * turn on" in place of Run now, and the button's description says why.
 */
const props = defineProps<{ automation: Automation; busy: boolean }>()
const emit = defineEmits<{
  confirm: []
  disable: []
  run: []
  edit: []
  runs: []
  remove: []
}>()

const { t } = useI18n()
const fmt = useFormat()
const words = useScheduleText()
const whyId = useId()

const state = computed(() => automationState(props.automation))
const schedule = computed(() => words.schedule(props.automation.schedule))
const nextRun = computed(() =>
  props.automation.nextRun
    ? words.runTime(props.automation.nextRun, props.automation.timezone)
    : '',
)
const last = computed(() => props.automation.lastRun)
// The task to open: the one running now, else the last run's own (a skipped run has none, F40).
const taskId = computed(() =>
  props.automation.running ? props.automation.activeTaskId : last.value?.taskId,
)
</script>

<template>
  <article class="card" :aria-labelledby="`automation-${automation.id}`">
    <header class="card__head">
      <h2 :id="`automation-${automation.id}`" class="card__name">{{ automation.name }}</h2>
      <StatusPill
        size="sm"
        :tone="STATE_LOOK[state].tone"
        :icon="STATE_LOOK[state].icon"
        :label="t(`automations.state.${state}`)"
      />
      <StatusPill
        v-if="automation.running"
        size="sm"
        tone="running"
        icon="statusRunning"
        :label="t('automations.card.running')"
      />
    </header>
    <p class="card__meta">
      <span :title="automation.schedule.cron || undefined">{{ schedule }}</span>
      <span v-if="automation.timezone !== 'local'">{{ automation.timezone }}</span>
      <code class="card__cwd">{{ automation.cwd }}</code>
    </p>
    <p class="card__prompt">{{ automation.prompt }}</p>
    <p class="card__meta">
      <span v-if="automation.enabled && nextRun">
        {{ t('automations.card.next', { when: nextRun }) }}
      </span>
      <span v-if="last" class="card__last">
        {{ t('automations.card.last', { when: fmt.relative(last.at) }) }}
        <StatusPill
          size="sm"
          :tone="OUTCOME_LOOK[last.outcome].tone"
          :icon="OUTCOME_LOOK[last.outcome].icon"
          :label="t(`automations.outcome.${last.outcome}`)"
        />
      </span>
      <RouterLink v-if="taskId" :to="{ name: 'tasks', params: { taskId } }">
        {{ t('automations.card.task') }}
      </RouterLink>
    </p>
    <p v-if="state === 'requested'" :id="whyId" class="card__ask" role="status">
      {{ t('automations.card.requested') }}
    </p>
    <p v-else-if="!automation.confirmed" :id="whyId" class="card__why">
      {{ t('automations.card.unconfirmed') }}
    </p>
    <div class="card__actions">
      <Button
        v-if="!automation.enabled"
        size="sm"
        variant="primary"
        :aria-describedby="automation.confirmed ? undefined : whyId"
        @click="emit('confirm')"
      >
        {{ automation.confirmed ? t('automations.card.enable') : t('automations.card.review') }}
      </Button>
      <Button v-else size="sm" :loading="busy" @click="emit('disable')">
        {{ t('automations.card.disable') }}
      </Button>
      <Button
        v-if="automation.confirmed"
        size="sm"
        icon="statusRunning"
        :disabled="busy || automation.running"
        @click="emit('run')"
      >
        {{ t('automations.card.runNow') }}
      </Button>
      <Button size="sm" variant="ghost" @click="emit('edit')">{{
        t('automations.card.edit')
      }}</Button>
      <Button size="sm" variant="ghost" @click="emit('runs')">{{
        t('automations.card.history')
      }}</Button>
      <Button size="sm" variant="ghost" icon="trash" @click="emit('remove')">
        {{ t('automations.card.delete') }}
      </Button>
    </div>
  </article>
</template>

<style scoped>
.card {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-4);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
}

.card__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.card__name {
  flex: 1 1 200px;
  min-width: 0;
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
  overflow-wrap: anywhere;
}

.card__meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) var(--space-3);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.card__meta a {
  color: var(--link);
}

@media (pointer: coarse) {
  .card__meta a {
    display: inline-flex;
    align-items: center;
    min-height: var(--target-min);
  }
}

.card__cwd {
  overflow-wrap: anywhere;
}

.card__last {
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1);
}

.card__prompt {
  display: -webkit-box;
  overflow: hidden;
  font-size: var(--fs-sm);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
  line-clamp: 3;
}

.card__ask {
  color: var(--attention-fg);
  font-size: var(--fs-sm);
}

.card__why {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.card__actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
</style>
