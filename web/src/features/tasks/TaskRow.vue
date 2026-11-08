<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import Icon from '@/components/ui/Icon.vue'
import { TASK_STATUS } from '@/components/ui/tone'
import { useFormat } from '@/composables/useFormat'
import { displayStatus, statusWordKey } from '@/lib/taskStatus'
import { useTasksStore } from '@/stores/tasks'

/**
 * One task in the list: status icon (with its word for screen readers), title, account and age. A link to the
 * task; in selection mode a click (Shift extends) and Space tick it instead. Reads its own task by id.
 */
const props = defineProps<{
  id: string
  now: number
  current: boolean
  focusable: boolean
  selectMode: boolean
  selected: boolean
  /** Show the project name (flat list, or the pinned "needs you" group). */
  showProject: boolean
}>()

const emit = defineEmits<{ toggle: [id: string, extend: boolean]; focus: [id: string] }>()

const { t } = useI18n()
const fmt = useFormat()
const tasks = useTasksStore()

const task = computed(() => tasks.get(props.id))
const status = computed(() => (task.value ? displayStatus(task.value) : 'queued'))
const look = computed(() => TASK_STATUS[status.value])

function onClick(event: MouseEvent, navigate: (e?: MouseEvent) => unknown) {
  if (props.selectMode) {
    event.preventDefault()
    emit('toggle', props.id, event.shiftKey)
    return
  }
  if (event.shiftKey) {
    event.preventDefault()
    emit('toggle', props.id, true)
    return
  }
  navigate(event)
}
</script>

<template>
  <li v-if="task" class="row-item" role="none">
    <RouterLink v-slot="{ href, navigate }" :to="{ name: 'tasks', params: { taskId: id } }" custom>
      <a
        :href="href"
        class="row"
        :class="{ 'row--current': current, 'row--selected': selected }"
        :data-key="`t:${id}`"
        data-row
        :tabindex="focusable ? 0 : -1"
        :aria-current="current ? 'page' : undefined"
        @click="onClick($event, navigate)"
        @focus="emit('focus', id)"
      >
        <span v-if="selectMode" class="row__check" :class="{ 'row__check--on': selected }">
          <Icon v-if="selected" name="check" :stroke-width="3" />
          <span class="sr-only">{{ t(selected ? 'tasks.select.on' : 'tasks.select.off') }}</span>
        </span>
        <Icon
          :name="look.icon"
          class="row__status"
          :class="`tone-${look.tone}`"
          :label="t(statusWordKey(task))"
        />
        <span class="row__body">
          <span class="row__title">{{ task.title }}</span>
          <span class="row__meta">
            <span v-if="showProject">{{ task.projectName }}</span>
            <span v-if="task.manual" class="row__manual">{{ t('tasks.row.manual') }}</span>
            <span v-else-if="task.accountLabel">{{ task.accountLabel }}</span>
          </span>
          <span
            v-if="!task.manual && task.activeStep && task.status === 'running'"
            class="row__step"
          >
            {{ task.activeStep.label }}
          </span>
        </span>
        <time class="row__time" :datetime="task.createdAt" :title="fmt.dateTime(task.createdAt)">
          {{ fmt.ago(task.createdAt, now, t('common.time.justNow')) }}
        </time>
      </a>
    </RouterLink>
  </li>
</template>

<style scoped>
.row-item {
  list-style: none;
}

.row {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  min-height: 48px;
  padding: var(--space-2) var(--space-3) var(--space-2) var(--space-4);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  text-decoration: none;
}

.row:hover {
  background: var(--bg-hover);
}

.row--current {
  background: var(--bg-active);
}

.row--selected {
  background: var(--accent-soft);
}

.row:focus-visible {
  outline: none;
  box-shadow: inset 0 0 0 var(--focus-ring-width) var(--border-focus);
}

.row__check {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  margin-top: 2px;
  border: var(--border-width) solid var(--border-strong);
  border-radius: var(--radius-xs);
  color: var(--text-on-accent);
  font-size: 12px;
}

.row__check--on {
  border-color: var(--accent);
  background: var(--accent);
}

.row__status {
  flex: none;
  margin-top: 2px;
  color: var(--tone-fg);
  font-size: var(--icon-md);
}

.row__body {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: var(--space-0);
  min-width: 0;
}

.row__title {
  display: -webkit-box;
  overflow: hidden;
  font-size: var(--fs-sm);
  line-height: var(--lh-tight);
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  overflow-wrap: anywhere;
}

.row__meta {
  display: flex;
  gap: var(--space-2);
  min-width: 0;
  color: var(--text-muted);
  font-size: var(--fs-2xs);
}

.row__meta > span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* The current step gets its own line, so a narrow sidebar never cuts project and step both. */
.row__step {
  overflow: hidden;
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: var(--fs-2xs);
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* On the current and selected rows' tinted backgrounds, muted text drops under 4.5:1; use secondary. */
.row--current .row__meta,
.row--current .row__step,
.row--current .row__time,
.row--selected .row__meta,
.row--selected .row__step,
.row--selected .row__time {
  color: var(--text-secondary);
}

.row__manual {
  color: var(--attention-fg);
}

.row__time {
  flex: none;
  color: var(--text-muted);
  font-size: var(--fs-2xs);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
</style>
