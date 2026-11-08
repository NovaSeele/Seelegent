<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { TaskSummary, Turn } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import StatusPill from '@/components/ui/StatusPill.vue'
import { useFormat } from '@/composables/useFormat'
import { useNow } from '@/composables/useNow'
import { displayStatus, isActive, isBlocked, statusWordKey } from '@/lib/taskStatus'

import DepLinks from './DepLinks.vue'
import { runTime } from './runTime'
import TaskHeaderActions from './TaskHeaderActions.vue'
import type { TaskActions } from './useTaskActions'

/**
 * The open task's title row (status, title, actions) and facts (project, model, account, turn, tokens, timing).
 * Actions show only when they apply; on phones the secondary ones fold into a menu.
 */
const props = defineProps<{
  task: TaskSummary
  titleLevel: 1 | 2
  showBack: boolean
  actions: TaskActions
  /** A peer objection stands: Accept is disabled until it is answered. */
  objected?: boolean
  /** The task's turns (from its detail), for the time the worker ran. */
  turns?: readonly Turn[]
}>()

const emit = defineEmits<{
  followup: []
  review: [verdict: 'accepted' | 'rejected']
  confirm: [action: 'cancel' | 'takeover']
}>()

const { t } = useI18n()
const fmt = useFormat()
const now = useNow(1000)

const status = computed(() => displayStatus(props.task))

// How long the worker ran (its turns), not how long the task waited for a review afterwards. Before the detail
// with its turns arrives, the task's age stands in.
const age = computed(() => {
  const ran = props.turns ? runTime(props.turns, now.value) : null
  if (ran !== null) return fmt.value.duration(ran)
  const end = isActive(props.task) ? now.value : Date.parse(props.task.updatedAt)
  return fmt.value.duration(Math.max(0, end - Date.parse(props.task.createdAt)))
})
</script>

<template>
  <header class="th">
    <div class="th__top">
      <Button
        v-if="showBack"
        size="sm"
        variant="ghost"
        icon="arrowLeft"
        :to="{ name: 'tasks' }"
        class="th__back"
      >
        {{ t('nav.tasks.label') }}
      </Button>
      <StatusPill :status="status" :label="t(statusWordKey(task))" />
      <span v-if="task.manual" class="th__manual">{{ t('task.header.driving') }}</span>
      <TaskHeaderActions
        :task="task"
        :actions="actions"
        :objected="objected"
        @followup="emit('followup')"
        @review="emit('review', $event)"
        @confirm="emit('confirm', $event)"
      />
    </div>
    <component :is="`h${titleLevel}`" class="th__title">{{ task.title }}</component>
    <p class="th__meta">
      <span :title="task.cwd">{{ task.projectName }}</span>
      <span v-if="task.role" class="th__chip">{{ task.role }}</span>
      <span
        >{{ task.model }}<template v-if="task.tier"> · {{ task.tier }}</template></span
      >
      <span>{{ task.accountLabel ?? t('common.worker.unassigned') }}</span>
      <span v-if="task.turnCount">{{ t('task.header.turn', { n: task.turnCount }) }}</span>
      <span v-if="task.tokensUsed">
        {{ t('task.header.tokens', { n: fmt.compact(task.tokensUsed) }) }}
      </span>
      <span>
        <time :datetime="task.createdAt" :title="fmt.dateTime(task.createdAt)">
          {{
            t('task.header.started', {
              time: fmt.ago(task.createdAt, now, t('common.time.justNow')),
            })
          }}
        </time>
        · {{ age }}
      </span>
      <span class="th__id">{{ task.id }}</span>
    </p>
    <p
      v-if="task.activeStep && task.status === 'running'"
      class="th__step"
      :class="{ 'th__step--attention': isBlocked(task) }"
    >
      <span class="th__step-label">{{ task.activeStep.label }}</span>
      <span class="th__step-time">
        {{ fmt.duration(Math.max(0, now - Date.parse(task.activeStep.since))) }}
      </span>
    </p>
    <p
      v-if="task.review"
      class="th__review"
      :class="`tone-${task.review.verdict === 'accepted' ? 'success' : 'danger'}`"
    >
      <strong>{{ t(`task.review.verdict.${task.review.verdict}`) }}</strong>
      <span v-if="task.review.notes"> · {{ task.review.notes }}</span>
    </p>
    <DepLinks :ids="task.after" />
  </header>
</template>

<style scoped>
.th {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-4) var(--space-5) var(--space-3);
}

.th__top {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.th__back {
  margin-left: calc(-1 * var(--space-2));
}

.th__manual {
  color: var(--attention-fg);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.th__title {
  color: var(--text-primary);
  font-size: var(--fs-xl);
  font-weight: var(--fw-semibold);
  line-height: var(--lh-tight);
  overflow-wrap: anywhere;
  text-wrap: balance;
}

.th__meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) var(--space-3);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.th__chip {
  padding: 0 var(--space-2);
  border-radius: var(--radius-pill);
  background: var(--neutral-bg);
  color: var(--neutral-fg);
}

.th__id {
  color: var(--text-muted);
  font-family: var(--font-mono);
}

.th__step {
  display: flex;
  gap: var(--space-2);
  max-width: 100%;
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm);
  background: var(--status-running-bg);
  color: var(--status-running-fg);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
}

.th__step--attention {
  background: var(--attention-bg);
  color: var(--attention-fg);
}

.th__step-label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.th__step-time {
  flex: none;
  font-variant-numeric: tabular-nums;
}

.th__review {
  color: var(--tone-fg);
  font-size: var(--fs-sm);
  overflow-wrap: anywhere;
}

@media (width < 600px) {
  .th {
    padding: var(--space-3) var(--space-4) var(--space-2);
  }

  .th__title {
    font-size: var(--fs-lg);
  }
}
</style>
