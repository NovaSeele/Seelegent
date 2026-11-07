<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { gitApi, type GitApi } from '@/api'
import type { TaskDetail, TaskSummary } from '@/api/types'
import Icon from '@/components/ui/Icon.vue'
import { isActive } from '@/lib/taskStatus'
import { useToastStore } from '@/stores/toast'

import OutsideDiffDialog from '../views/git/OutsideDiffDialog.vue'
import {
  outsideTurns,
  restoredByTurn,
  safetyCount,
  safetyOf,
  safetyParts,
  safetyUrgent,
} from './safety'
import SafetyChecks from './SafetyChecks.vue'
import SafetyRisk from './SafetyRisk.vue'

/**
 * What the safety checks found for the task: work at risk (with Restore and the turn's changes outside the
 * edit tools), files other tasks of the repository changed at the same time, the latest turn's hygiene and
 * scope findings, and the commands the git guard refused. A disclosure, open while something needs a look;
 * hidden when there is nothing to say. `changed` asks the parent to read the task again after a restore.
 */
const props = withDefaults(
  defineProps<{ task: TaskSummary; detail: TaskDetail | null; api?: GitApi }>(),
  { api: () => gitApi },
)
const emit = defineEmits<{ changed: [] }>()

const { t } = useI18n()
const toast = useToastStore()

const safety = computed(() => safetyOf(props.detail))
const parts = computed(() => safetyParts(safety.value))
const count = computed(() => safetyCount(safety.value))
const urgent = computed(() => safetyUrgent(safety.value))
const active = computed(() => isActive(props.task))

/** The collapsed line after the count: one short phrase per kind, restored work last and muted. */
const summary = computed(() => {
  const p = parts.value
  const phrase = (kind: keyof typeof p) => ({
    kind,
    text: t(`task.safety.parts.${kind}`, { count: p[kind] }, p[kind]),
    muted: kind === 'restored',
  })
  return (['risk', 'overlaps', 'findings', 'blocked', 'restored'] as const)
    .filter((kind) => p[kind] > 0)
    .map(phrase)
})

// Opens by itself when something becomes urgent; the user's own toggle wins otherwise.
const expanded = ref(urgent.value)
watch(urgent, (value) => {
  if (value) expanded.value = true
})

const restoring = ref<number | null>(null)
async function restore(turn: number) {
  restoring.value = turn
  try {
    const result = await props.api.restore(props.task.id, turn)
    if (result.restored.length) {
      toast.show({
        message: t('task.safety.risk.done', { n: result.restored.length }),
        tone: 'success',
      })
    }
    if (result.skipped.length) {
      toast.show({
        message: t('task.safety.risk.skipped', {
          files: result.skipped.map((s) => `${s.path}: ${s.error}`).join('; '),
        }),
        tone: 'error',
      })
    }
    emit('changed')
  } catch (err) {
    toast.show({
      message: t('task.actions.failed', {
        error: err instanceof Error ? err.message : String(err),
      }),
      tone: 'error',
    })
  } finally {
    restoring.value = null
  }
}

const outsideOpen = ref(false)
const outsideTurn = ref<number | undefined>(undefined)
const turns = computed(() => outsideTurns(props.detail))
const restored = computed(() => restoredByTurn(props.detail))
function showOutside(turn: number) {
  outsideTurn.value = turn
  outsideOpen.value = true
}
</script>

<template>
  <details
    v-if="count || parts.restored"
    class="safety"
    :class="urgent ? 'tone-danger' : 'tone-neutral'"
    :open="expanded"
    @toggle="expanded = ($event.target as HTMLDetailsElement).open"
  >
    <summary class="safety__summary">
      <Icon :name="urgent ? 'warning' : 'info'" class="safety__icon" />
      <strong>{{ t('task.safety.title') }}</strong>
      <span v-if="count" class="safety__count">{{ t('task.safety.count', { n: count }) }}</span>
      <span class="safety__parts">
        <span
          v-for="part in summary"
          :key="part.kind"
          class="safety__phrase"
          :class="{ 'safety__phrase--muted': part.muted }"
        >
          {{ part.text }}
        </span>
      </span>
    </summary>
    <div class="safety__body">
      <SafetyRisk
        v-if="safety.risk.length"
        :groups="safety.risk"
        :busy="restoring"
        :active="active"
        @restore="restore"
        @outside="showOutside"
      />
      <SafetyChecks :safety="safety" />
    </div>
  </details>
  <OutsideDiffDialog
    v-if="outsideOpen"
    v-model:open="outsideOpen"
    :task-id="task.id"
    :turns="turns"
    :turn="outsideTurn"
    :restored="restored"
    :api="api"
  />
</template>

<style scoped>
.safety {
  margin: 0 var(--space-5) var(--space-3);
  border: var(--border-width) solid var(--tone-border);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
}

.safety__summary {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) var(--space-2);
  min-height: var(--target-min);
  padding: var(--space-2) var(--space-4);
  cursor: pointer;
  font-size: var(--fs-sm);
}

.safety__summary:focus-visible {
  border-radius: var(--radius-lg);
  outline: none;
  box-shadow: var(--focus-ring);
}

.safety__icon {
  color: var(--tone-fg);
}

.safety__count {
  color: var(--text-secondary);
}

.safety__parts {
  display: inline-flex;
  flex-wrap: wrap;
  gap: 0 var(--space-1);
  min-width: 0;
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

/* "1 shared file · 1 command refused": a middle dot between phrases, none before the first. */
.safety__phrase + .safety__phrase::before {
  margin-right: var(--space-1);
  content: '·';
}

.safety__phrase--muted {
  color: var(--text-muted);
}

.safety__body {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  max-height: 40vh;
  padding: 0 var(--space-4) var(--space-3);
  overflow-y: auto;
}

@media (height < 760px) {
  .safety__body {
    max-height: 28vh;
  }
}

@media (width < 600px) {
  .safety {
    margin: 0 var(--space-4) var(--space-3);
  }
}
</style>
