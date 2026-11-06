<script setup lang="ts">
import { computed, ref, shallowReactive } from 'vue'
import { useI18n } from 'vue-i18n'

import type { TaskDetail, TaskSummary } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Icon from '@/components/ui/Icon.vue'

import ProcessFrame from './ProcessFrame.vue'
import StepRow from './StepRow.vue'
import { useProcess } from './useProcess'

/**
 * The Tree view: turns, their attempts and the steps of each (stage, tool, file or command, duration). A step
 * opens in the Terminal; Up/Down move between steps.
 */
const props = defineProps<{ task: TaskSummary; detail: TaskDetail | null }>()
defineEmits<{ reveal: [key: string] }>()

const { t } = useI18n()
const model = useProcess(props.task.id, () => props.detail)
const turns = model.turns

const collapsed = shallowReactive(new Set<number>())
const allOpen = computed(() => collapsed.size === 0)
function toggleTurn(n: number) {
  if (collapsed.has(n)) collapsed.delete(n)
  else collapsed.add(n)
}
function setAll(open: boolean) {
  collapsed.clear()
  if (!open) for (const turn of turns.value) collapsed.add(turn.n)
}

// Attempts that ended badly say why (the Terminal's wording for the same event).
const NOTED = new Set(['quota', 'auth', 'transient', 'background_wait', 'error'])
const noteOf = (status: string | undefined) =>
  status && NOTED.has(status) ? `terminal.ev.attempt.${status}` : ''

const stepCount = (turn: (typeof turns.value)[number]) =>
  turn.attempts.reduce((sum, attempt) => sum + attempt.steps.length, 0)

const root = ref<HTMLElement | null>(null)
function onKeydown(event: KeyboardEvent) {
  if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return
  const buttons = [...(root.value?.querySelectorAll<HTMLElement>('[data-step], [data-turn]') ?? [])]
  const index = buttons.indexOf(document.activeElement as HTMLElement)
  const next = buttons[index + (event.key === 'ArrowDown' ? 1 : -1)]
  if (!next) return
  event.preventDefault()
  next.focus()
}
</script>

<template>
  <ProcessFrame
    :model="model"
    :empty="!model.steps.value.length"
    icon="tree"
    :empty-title="t('process.empty')"
    :empty-desc="t('process.emptyDesc')"
  >
    <template #bar>
      <Button size="sm" variant="ghost" @click="setAll(!allOpen)">
        {{ allOpen ? t('process.tree.collapseAll') : t('process.tree.expandAll') }}
      </Button>
    </template>
    <ol ref="root" class="tree" :aria-label="t('process.tree.label')" @keydown="onKeydown">
      <li v-for="turn in turns" :key="turn.n" class="tree__turn">
        <button
          type="button"
          class="tree__head"
          data-turn
          :aria-expanded="!collapsed.has(turn.n)"
          @click="toggleTurn(turn.n)"
        >
          <Icon :name="collapsed.has(turn.n) ? 'chevronRight' : 'chevronDown'" class="tree__chev" />
          <span class="tree__title">{{ t('process.turn', { n: turn.n }) }}</span>
          <span v-if="turn.by" class="tree__by">{{ t(`terminal.by.${turn.by}`) }}</span>
          <span class="tree__msg">{{ turn.message }}</span>
          <span v-if="model.notLoaded(turn.n)" class="tree__count">
            {{ t('process.tree.notLoaded') }}
          </span>
          <span v-else class="tree__count">{{ t('process.steps', stepCount(turn)) }}</span>
        </button>
        <div v-if="!collapsed.has(turn.n)" class="tree__body">
          <section v-for="attempt in turn.attempts" :key="attempt.n" class="tree__attempt">
            <p v-if="turn.attempts.length > 1 || attempt.accountLabel" class="tree__attempt-head">
              <span>{{ t('process.attempt', { n: attempt.n }) }}</span>
              <span v-if="attempt.accountLabel">· {{ attempt.accountLabel }}</span>
              <span v-if="attempt.model">· {{ attempt.model }}</span>
              <span v-if="noteOf(attempt.status)" class="tree__attempt-status">
                · {{ t(noteOf(attempt.status)) }}
              </span>
            </p>
            <ul class="tree__steps">
              <StepRow
                v-for="step in attempt.steps"
                :key="step.key"
                :step="step"
                :cwd="task.cwd"
                @reveal="$emit('reveal', $event)"
              />
            </ul>
          </section>
          <div v-if="model.notLoaded(turn.n)" class="tree__none">
            <span>{{ t('process.tree.notLoadedDesc') }}</span>
            <Button
              size="sm"
              variant="ghost"
              :loading="model.state.loadingEarlier"
              @click="model.loadEarlier"
            >
              {{ t('process.loadEarlier') }}
            </Button>
          </div>
          <p v-else-if="!stepCount(turn)" class="tree__none">{{ t('process.tree.noSteps') }}</p>
        </div>
      </li>
    </ol>
  </ProcessFrame>
</template>

<style scoped>
.tree {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-4) var(--space-5);
  list-style: none;
}

.tree__head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  min-height: var(--control-md);
  padding: 0 var(--space-2);
  border: 0;
  border-radius: var(--radius-sm);
  background: var(--bg-surface-2);
  color: var(--text-primary);
  font: inherit;
  font-size: var(--fs-sm);
  text-align: left;
  cursor: pointer;
}

.tree__head:hover {
  background: var(--bg-hover);
}

.tree__head:focus-visible {
  outline: none;
  box-shadow: inset 0 0 0 var(--focus-ring-width) var(--border-focus);
}

.tree__chev {
  flex: none;
  font-size: var(--icon-sm);
}

.tree__title {
  flex: none;
  font-weight: var(--fw-semibold);
}

.tree__by {
  flex: none;
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.tree__msg {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  color: var(--text-secondary);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tree__count {
  flex: none;
  color: var(--text-muted);
  font-size: var(--fs-xs);
}

.tree__body {
  padding: var(--space-1) 0 0 var(--space-5);
}

.tree__attempt-head {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  padding: var(--space-1) var(--space-2);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.tree__attempt-status {
  color: var(--status-danger-fg);
}

.tree__steps {
  list-style: none;
}

.tree__none {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-1) var(--space-2);
  color: var(--text-muted);
  font-size: var(--fs-xs);
}

@media (width < 600px) {
  .tree {
    padding-inline: var(--space-2);
  }

  .tree__body {
    padding-left: var(--space-2);
  }

  .tree__msg {
    display: none;
  }
}
</style>
