<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import { gitApi, type GitApi } from '@/api'
import type { TaskDetail, TaskSummary } from '@/api/types'
import Chip from '@/components/ui/Chip.vue'
import Icon from '@/components/ui/Icon.vue'
import { isActive } from '@/lib/taskStatus'
import { useToastStore } from '@/stores/toast'

import OutsideDiffDialog from '../views/git/OutsideDiffDialog.vue'
import { findingKey, safetyCount, safetyOf, safetyUrgent } from './safety'
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
const count = computed(() => safetyCount(safety.value))
const urgent = computed(() => safetyUrgent(safety.value))
const active = computed(() => isActive(props.task))

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
const outsideTurns = computed(() =>
  (props.detail?.turns ?? [])
    .filter((turn) => turn.outsideDiff)
    .map((turn) => turn.n)
    .sort((a, b) => b - a),
)
function showOutside(turn: number) {
  outsideTurn.value = turn
  outsideOpen.value = true
}
</script>

<template>
  <details
    v-if="count"
    class="safety"
    :class="urgent ? 'tone-danger' : 'tone-neutral'"
    :open="expanded"
    @toggle="expanded = ($event.target as HTMLDetailsElement).open"
  >
    <summary class="safety__summary">
      <Icon name="warning" class="safety__icon" />
      <strong>{{ t('task.safety.title') }}</strong>
      <span class="safety__count">{{ t('task.safety.count', { n: count }) }}</span>
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
      <section v-if="safety.overlaps.length" class="safety__part">
        <h3 class="safety__title">{{ t('task.safety.overlaps.title') }}</h3>
        <ul class="safety__list">
          <li v-for="o in safety.overlaps" :key="`${o.taskId}:${o.file}`" class="safety__item">
            <code class="safety__path">{{ o.file }}</code>
            <RouterLink :to="{ name: 'tasks', params: { taskId: o.taskId } }" class="safety__link">
              {{ o.title || o.taskId }}
            </RouterLink>
            <Chip :tone="o.severity === 'active' ? 'danger' : 'neutral'">
              {{ t(`task.safety.overlaps.${o.severity}`) }}
            </Chip>
          </li>
        </ul>
      </section>
      <section v-if="safety.findings.length" class="safety__part">
        <h3 class="safety__title">
          {{ t('task.safety.findings', { n: safety.findingsTurn ?? 0 }) }}
        </h3>
        <ul class="safety__list">
          <li v-for="(f, i) in safety.findings" :key="i" class="safety__item">
            <Chip :tone="f.severity === 'red' ? 'danger' : 'attention'">
              {{ t(`task.safety.severity.${f.severity}`) }}
            </Chip>
            <span>{{ t(findingKey(f.kind)) }}</span>
            <code v-if="f.path" class="safety__path">{{ f.path }}</code>
            <span v-if="f.detail" class="safety__detail">{{ f.detail }}</span>
          </li>
        </ul>
      </section>
      <section v-if="safety.blocked.length" class="safety__part">
        <h3 class="safety__title">{{ t('task.safety.blocked') }}</h3>
        <ul class="safety__list">
          <li v-for="(b, i) in safety.blocked" :key="i" class="safety__item">
            <span class="safety__detail">{{ t('task.safety.turn', { n: b.turn }) }}</span>
            <code class="safety__path">{{ b.command }}</code>
          </li>
        </ul>
      </section>
    </div>
  </details>
  <OutsideDiffDialog
    v-if="outsideOpen"
    v-model:open="outsideOpen"
    :task-id="task.id"
    :turns="outsideTurns"
    :turn="outsideTurn"
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
  align-items: center;
  gap: var(--space-2);
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

.safety__body {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  max-height: 40vh;
  padding: 0 var(--space-4) var(--space-3);
  overflow-y: auto;
}

.safety__part {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.safety__title {
  font-size: var(--fs-sm);
  font-weight: var(--fw-semibold);
}

.safety__list {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin: 0;
  padding: 0;
  list-style: none;
}

.safety__item {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-1) var(--space-2);
  font-size: var(--fs-sm);
}

.safety__path {
  min-width: 0;
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  overflow-wrap: anywhere;
}

.safety__link {
  color: var(--accent);
}

.safety__detail {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  overflow-wrap: anywhere;
}

@media (width < 600px) {
  .safety {
    margin: 0 var(--space-4) var(--space-3);
  }
}
</style>
