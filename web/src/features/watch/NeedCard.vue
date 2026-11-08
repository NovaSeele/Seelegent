<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import Button from '@/components/ui/Button.vue'
import Icon from '@/components/ui/Icon.vue'
import { useFormat } from '@/composables/useFormat'
import { attentionText } from '@/features/tasks/detail/attentionText'
import { NEED_WORD_KEY, type NeedKind, statusWordKey } from '@/lib/taskStatus'
import { useTasksStore } from '@/stores/tasks'

import { NEED_LOOK, nextStep, OVERDUE_MS } from './watchModel'

/** One task that waits for you: why, since when, and the next step. Reads its own task from the store. */
const props = defineProps<{ id: string; kind: NeedKind; since: string; now: number }>()

const { t, te } = useI18n()
const fmt = useFormat()
const tasks = useTasksStore()

const task = computed(() => tasks.get(props.id))
const look = computed(() => NEED_LOOK[props.kind])
const waited = computed(() => Math.max(0, props.now - Date.parse(props.since)))
const overdue = computed(() => waited.value >= OVERDUE_MS)
const step = computed(() => nextStep(props.kind))
/** A blocked task names why when the pill does (a peer's objection); other kinds keep the board's word. */
const word = computed(() =>
  props.kind === 'blocked' && task.value ? statusWordKey(task.value) : NEED_WORD_KEY[props.kind],
)

/**
 * First line of why, in the reader's language (F14): the worker's question as it asked it, else the sentence built
 * from the flag's reason and params. The daemon's English detail is not shown here.
 */
const detail = computed(() => {
  const a = task.value?.attention
  if (!a) return ''
  const view = attentionText(a, t, te)
  const text = a.reason === 'question' ? (a.detail ?? '') : view.why || view.quote
  return (
    text
      .split('\n')
      .find((line) => line.trim())
      ?.trim() ?? ''
  )
})

const target = computed(() =>
  step.value === 'review'
    ? { name: 'tasks', params: { taskId: props.id }, query: { view: 'git' } }
    : { name: 'tasks', params: { taskId: props.id } },
)
</script>

<template>
  <article v-if="task" class="need" :class="`tone-${look.tone}`" :data-need="kind">
    <header class="need__head">
      <span class="need__kind">
        <Icon :name="look.icon" class="need__icon" />
        {{ t(word) }}
      </span>
      <span class="need__age" :class="{ 'need__age--overdue': overdue }">
        <time :datetime="since" :title="fmt.dateTime(since)">
          {{ t('watch.needs.waiting', { time: fmt.duration(waited) }) }}
        </time>
      </span>
    </header>
    <h3 class="need__title">
      <RouterLink :to="{ name: 'tasks', params: { taskId: id } }" class="need__link">
        {{ task.title }}
      </RouterLink>
    </h3>
    <p class="need__meta">
      <span>{{ task.projectName }}</span>
      <span v-if="task.accountLabel">· {{ task.accountLabel }}</span>
    </p>
    <p v-if="detail" class="need__detail">{{ detail }}</p>
    <div class="need__actions">
      <Button size="sm" :variant="step === 'open' ? 'secondary' : 'primary'" :to="target">
        {{ t(`watch.next.${step}`) }}
      </Button>
    </div>
  </article>
</template>

<style scoped>
.need {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
  padding: var(--space-3) var(--space-4) var(--space-3) calc(var(--space-4) + var(--status-stripe));
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
  box-shadow: var(--shadow-sm);
}

.need::before {
  content: '';
  position: absolute;
  inset: var(--space-3) auto var(--space-3) 0;
  width: var(--status-stripe);
  border-radius: 0 var(--radius-pill) var(--radius-pill) 0;
  background: var(--tone-fg);
}

.need:focus-within {
  border-color: var(--tone-border);
}

.need__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-1) var(--space-3);
  font-size: var(--fs-xs);
}

.need__kind {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  color: var(--tone-fg);
  font-weight: var(--fw-semibold);
}

.need__icon {
  font-size: var(--icon-sm);
}

.need__age {
  color: var(--text-muted);
  font-variant-numeric: tabular-nums;
}

.need__age--overdue {
  color: var(--attention-fg);
  font-weight: var(--fw-medium);
}

.need__title {
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
  line-height: var(--lh-tight);
}

.need__link {
  color: var(--text-primary);
  text-decoration: none;
  overflow-wrap: anywhere;
}

.need__link:hover {
  text-decoration: underline;
}

/* The whole card is the link's target area; the action button stays above it. */
.need__link::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: inherit;
}

.need__link:focus-visible {
  outline: none;
}

.need__link:focus-visible::after {
  box-shadow: var(--focus-ring);
}

.need__meta {
  display: flex;
  flex-wrap: wrap;
  gap: 0 var(--space-1);
  color: var(--text-muted);
  font-size: var(--fs-xs);
  overflow-wrap: anywhere;
}

.need__detail {
  display: -webkit-box;
  overflow: hidden;
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  line-height: var(--lh-ui);
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  overflow-wrap: anywhere;
}

.need__actions {
  position: relative;
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-top: var(--space-2);
}
</style>
