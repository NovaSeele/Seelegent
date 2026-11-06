<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { TaskSummary } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import IconButton from '@/components/ui/IconButton.vue'
import Menu, { type MenuItem } from '@/components/ui/Menu.vue'
import StatusPill from '@/components/ui/StatusPill.vue'
import { useFormat } from '@/composables/useFormat'
import { useBreakpoint } from '@/composables/useMediaQuery'
import { useNow } from '@/composables/useNow'
import {
  awaitsReview,
  canCancel,
  canFollowup,
  canTakeOver,
  displayStatus,
  isActive,
  isBlocked,
  statusWordKey,
} from '@/lib/taskStatus'

import { TASK_KEYS } from './taskKeys'
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
}>()

const emit = defineEmits<{
  followup: []
  review: [verdict: 'accepted' | 'rejected']
  confirm: [action: 'cancel' | 'takeover']
}>()

const { t } = useI18n()
const fmt = useFormat()
const now = useNow(1000)
const { isMobile } = useBreakpoint()

const status = computed(() => displayStatus(props.task))
const reviewable = computed(() => awaitsReview(props.task))
const age = computed(() => {
  const end = isActive(props.task) ? now.value : Date.parse(props.task.updatedAt)
  return fmt.value.duration(Math.max(0, end - Date.parse(props.task.createdAt)))
})

const menuItems = computed<MenuItem[]>(() => {
  const items: MenuItem[] = []
  if (canFollowup(props.task)) {
    items.push({ id: 'followup', label: t('task.actions.followup'), icon: 'send' })
  }
  if (canTakeOver(props.task)) {
    items.push({ id: 'takeover', label: t('task.actions.takeover'), icon: 'hand' })
  }
  items.push({ id: 'copy', label: t('task.actions.copyId'), icon: 'copy' })
  if (canCancel(props.task)) {
    items.push({ id: 'cancel', label: t('task.actions.cancel'), icon: 'x', danger: true })
  }
  return items
})

function onMenu(id: string) {
  if (id === 'followup') emit('followup')
  else if (id === 'takeover') emit('confirm', 'takeover')
  else if (id === 'cancel') emit('confirm', 'cancel')
  else if (id === 'copy') void props.actions.copyId()
}
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
      <div class="th__actions">
        <template v-if="reviewable">
          <Button
            size="sm"
            variant="primary"
            icon="check"
            :loading="actions.busy.value === 'review'"
            :aria-keyshortcuts="TASK_KEYS.accept"
            @click="emit('review', 'accepted')"
          >
            {{ t('task.actions.accept') }}
          </Button>
          <Button
            size="sm"
            icon="statusRejected"
            :aria-keyshortcuts="TASK_KEYS.reject"
            @click="emit('review', 'rejected')"
          >
            {{ t('task.actions.reject') }}
          </Button>
        </template>
        <template v-if="!isMobile">
          <IconButton
            v-if="canFollowup(task)"
            icon="send"
            size="sm"
            variant="secondary"
            :label="t('task.actions.followup')"
            :shortcut="TASK_KEYS.followup"
            @click="emit('followup')"
          />
          <IconButton
            v-if="canTakeOver(task)"
            icon="hand"
            size="sm"
            variant="secondary"
            :label="t('task.actions.takeover')"
            :shortcut="TASK_KEYS.takeover"
            @click="emit('confirm', 'takeover')"
          />
          <IconButton
            v-if="canCancel(task)"
            icon="x"
            size="sm"
            variant="secondary"
            :label="t('task.actions.cancel')"
            :shortcut="TASK_KEYS.cancel"
            @click="emit('confirm', 'cancel')"
          />
          <IconButton
            icon="copy"
            size="sm"
            variant="secondary"
            :label="t('task.actions.copyId')"
            @click="actions.copyId()"
          />
        </template>
        <Menu v-else :items="menuItems" :label="t('task.actions.more')" @select="onMenu">
          <template #trigger="{ attrs }">
            <IconButton
              v-bind="attrs"
              icon="more"
              size="sm"
              variant="secondary"
              :label="t('task.actions.more')"
              :tooltip="false"
            />
          </template>
        </Menu>
      </div>
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
          {{ t('task.header.started', { time: fmt.relative(task.createdAt, now) }) }}
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
    <p v-if="task.after.length" class="th__deps">
      {{ t('task.header.after') }}
      <RouterLink
        v-for="dep in task.after"
        :key="dep"
        :to="{ name: 'tasks', params: { taskId: dep } }"
        class="th__dep"
      >
        {{ dep }}
      </RouterLink>
      <span v-if="task.depsReason"> · {{ task.depsReason }}</span>
    </p>
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

.th__actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  margin-left: auto;
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

.th__deps {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-2);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.th__dep {
  color: var(--accent);
  font-family: var(--font-mono);
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
