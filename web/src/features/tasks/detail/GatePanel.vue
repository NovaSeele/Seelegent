<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { TaskDetail, TaskSummary } from '@/api/types'
import Icon from '@/components/ui/Icon.vue'
import Spinner from '@/components/ui/Spinner.vue'

import { budgetUsedUp, fixesUsed, gateOf, gateTone, gateUrgent, hasGate } from './gate'
import GateFacts from './GateFacts.vue'
import GateSteps from './GateSteps.vue'

/**
 * The task's gate (docs/gates.md): what it is doing now (running, settling, the second-opinion review), the
 * steps of the latest run with the end of their output, the automatic fix turns, the verifier's verdict, and what
 * the gate checks (lane, commands, doctrine, the frozen bar). A disclosure that opens by itself when the gate
 * fails or the verifier rejects; hidden for a task without a gate.
 */
const props = defineProps<{ task: TaskSummary; detail: TaskDetail | null }>()

const { t } = useI18n()

const view = computed(() => gateOf(props.task, props.detail))
const tone = computed(() => gateTone(view.value.state, view.value.phase))
const urgent = computed(() => gateUrgent(view.value))
/** The fixes are used up and the gate still fails: only the lead or the user can move it now. */
const usedUp = computed(
  () =>
    budgetUsedUp(view.value) &&
    !view.value.phase &&
    (view.value.state === 'red' || view.value.state === 'error'),
)

const stateText = computed(() => {
  const v = view.value
  if (v.phase) return t(`task.gate.phase.${v.phase}`)
  if (v.state) return t(`task.gate.state.${v.state}`)
  return t('task.gate.state.none')
})

const expanded = ref(urgent.value)
watch(urgent, (value) => {
  if (value) expanded.value = true
})
</script>

<template>
  <details
    v-if="hasGate(view)"
    class="gate"
    :class="`tone-${tone}`"
    :open="expanded"
    @toggle="expanded = ($event.target as HTMLDetailsElement).open"
  >
    <summary class="gate__summary">
      <Spinner v-if="view.phase" size="sm" decorative />
      <Icon v-else :name="urgent ? 'warning' : 'info'" class="gate__icon" />
      <strong>{{ t('task.gate.title') }}</strong>
      <span class="gate__state">{{ stateText }}</span>
      <span class="gate__parts">
        <span v-if="view.turn" class="gate__phrase">{{
          t('task.gate.turn', { n: view.turn })
        }}</span>
        <span v-if="usedUp" class="gate__phrase">
          {{ t('task.gate.usedUp', { n: fixesUsed(view), max: view.proof?.budget ?? 0 }) }}
        </span>
        <span v-else-if="view.fixTurns" class="gate__phrase">
          {{ t('task.gate.fixTurns', { count: view.fixTurns }, view.fixTurns) }}
        </span>
        <span v-if="view.verifier" class="gate__phrase">
          {{ t(`task.gate.verifier.short.${view.verifier.verdict}`) }}
        </span>
        <span v-if="view.proof?.barChanged.length" class="gate__phrase">
          {{ t('task.gate.barShort') }}
        </span>
      </span>
    </summary>
    <div class="gate__body">
      <p v-if="usedUp" class="gate__muted">{{ t('task.gate.nextStep') }}</p>
      <GateSteps v-if="view.result" :result="view.result" :turn="view.turn" />
      <p v-else class="gate__muted">{{ t('task.gate.notYet') }}</p>
      <GateFacts :proof="view.proof" :verifier="view.verifier" />
    </div>
  </details>
</template>

<style scoped>
.gate {
  margin: 0 var(--space-5) var(--space-3);
  border: var(--border-width) solid var(--tone-border);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
}

.gate__summary {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) var(--space-2);
  min-height: var(--target-min);
  padding: var(--space-2) var(--space-4);
  cursor: pointer;
  font-size: var(--fs-sm);
}

.gate__summary:focus-visible {
  border-radius: var(--radius-lg);
  outline: none;
  box-shadow: var(--focus-ring);
}

.gate__icon,
.gate__state {
  color: var(--tone-fg);
}

.gate__parts {
  display: inline-flex;
  flex-wrap: wrap;
  gap: 0 var(--space-1);
  min-width: 0;
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.gate__phrase + .gate__phrase::before {
  margin-right: var(--space-1);
  content: '·';
}

.gate__body {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  max-height: 45vh;
  padding: 0 var(--space-4) var(--space-3);
  overflow-y: auto;
}

.gate__muted {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

@media (height < 760px) {
  .gate__body {
    max-height: 30vh;
  }
}

@media (width < 600px) {
  .gate {
    margin: 0 var(--space-4) var(--space-3);
  }

  /* The page scrolls on phones: no scroll box inside it. */
  .gate__body {
    max-height: none;
  }
}
</style>
