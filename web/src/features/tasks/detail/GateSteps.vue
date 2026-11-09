<script setup lang="ts">
import { computed } from 'vue'
import { I18nT, useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import type { GateResult } from '@/api/types'
import Chip from '@/components/ui/Chip.vue'
import Icon from '@/components/ui/Icon.vue'
import { useFormat } from '@/composables/useFormat'

import { LOW_MEMORY_STOPS_MAX, stepBadge, stepCommand, stepNote } from './gate'

/**
 * The steps of one gate run: each with its state, command, exit code and time, the files it is about, and the end
 * of its output (open for a failing step). Worker and command output is shown as text, never as HTML. A step the
 * memory watchdog stopped says so (with the stops of this gate phase) instead of failing, and a run that ended in
 * error because the machine kept running out of memory says what to do next, with a link to the Resources settings.
 */
const props = defineProps<{ result: GateResult; turn: number | null }>()

const { t } = useI18n()
const fmt = useFormat()

/** The step rows already say which steps did not run: the extra line is only for runs that list none. */
const notRunShown = computed(() => props.result.steps.some((s) => s.state === 'not_run'))
</script>

<template>
  <section class="steps">
    <h3 class="steps__title">
      {{ turn ? t('task.gate.stepsOfTurn', { n: turn }) : t('task.gate.steps') }}
    </h3>
    <ol class="steps__list">
      <li v-for="(step, i) in result.steps" :key="i" class="steps__item">
        <div class="steps__row">
          <Chip :tone="stepBadge(step).tone">{{ t(stepBadge(step).key) }}</Chip>
          <span class="steps__label">{{ t(`task.gate.kind.${step.kind}`) }}</span>
          <code v-if="step.cmd" class="steps__cmd">{{ stepCommand(step) }}</code>
          <span v-else-if="stepNote(step)" class="steps__muted">{{ stepNote(step) }}</span>
          <span v-if="step.exit !== undefined" class="steps__muted">
            {{ t('task.gate.exit', { n: step.exit }) }}
          </span>
          <span v-if="step.ms && step.ms >= 1000" class="steps__muted">{{
            fmt.duration(step.ms)
          }}</span>
          <span v-if="step.timedOut" class="steps__muted">{{ t('task.gate.timedOut') }}</span>
          <span v-if="step.taskIds?.length" class="steps__muted">{{
            step.taskIds.join(', ')
          }}</span>
        </div>
        <p v-if="step.stopped === 'low_memory'" class="steps__note" data-stopped>
          {{ t('task.gate.stoppedLowMemory', { n: step.stops ?? 1, max: LOW_MEMORY_STOPS_MAX }) }}
        </p>
        <I18nT v-if="step.foreign" keypath="task.gate.foreign" tag="p" class="steps__note">
          <template #tasks>
            <template v-for="(c, ci) in step.causedBy ?? []" :key="c.taskId"
              ><template v-if="ci">, </template
              ><RouterLink
                :to="{ name: 'tasks', params: { taskId: c.taskId } }"
                class="steps__link"
                >{{ c.title || c.taskId }}</RouterLink
              ></template
            >
          </template>
        </I18nT>
        <ul v-if="step.files?.length && step.kind === 'lane'" class="steps__files">
          <li v-for="file in step.files" :key="file">
            <code>{{ file }}</code>
          </li>
        </ul>
        <ul v-if="step.failures?.length" class="steps__files">
          <li v-for="f in step.failures" :key="f.file">
            <code>{{ f.file }}</code>
            <span class="steps__muted">{{ f.message }}</span>
          </li>
        </ul>
        <details
          v-if="step.tail && step.kind !== 'lane'"
          class="steps__tail"
          :open="
            (step.state === 'red' || step.state === 'error') &&
            !step.failures?.length &&
            !step.stopped
          "
        >
          <summary class="steps__tail-summary">
            <Icon name="chevronRight" class="steps__chevron" />{{ t('task.gate.output') }}
          </summary>
          <pre class="steps__pre">{{ step.tail }}</pre>
        </details>
      </li>
    </ol>
    <p v-if="result.notRun.length && !notRunShown" class="steps__muted">
      {{ t('task.gate.notRun', { steps: result.notRun.join(', ') }) }}
    </p>
    <p v-if="result.reason === 'low_memory'" class="steps__note" data-reason>
      {{ t('task.gate.lowMemory', { max: LOW_MEMORY_STOPS_MAX }) }}
      {{ t('task.gate.lowMemoryNext', { setting: t('settings.items.gateConcurrency.label') }) }}
      <RouterLink :to="{ name: 'settings', params: { section: 'resources' } }" class="steps__link">
        {{ t('nav.lowMemory.settings') }}
      </RouterLink>
    </p>
    <p v-if="result.settled" class="steps__muted">{{ t('task.gate.settled') }}</p>
    <ul v-if="result.warnings.length" class="steps__warnings">
      <li v-for="(w, i) in result.warnings" :key="i">{{ w }}</li>
    </ul>
  </section>
</template>

<style scoped>
.steps {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.steps__title {
  font-size: var(--fs-sm);
  font-weight: var(--fw-semibold);
}

.steps__list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.steps__item {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.steps__row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) var(--space-2);
  font-size: var(--fs-sm);
}

.steps__label {
  font-weight: var(--fw-medium);
}

.steps__cmd,
.steps__files code {
  min-width: 0;
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  overflow-wrap: anywhere;
}

.steps__muted,
.steps__note,
.steps__warnings {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  overflow-wrap: anywhere;
}

.steps__link {
  color: var(--accent);
}

/* A touch target of at least 44px around the link, without changing how it looks. */
@media (pointer: coarse) {
  .steps__link {
    position: relative;
  }

  .steps__link::after {
    position: absolute;
    inset: 50% 0 auto;
    min-width: var(--target-min);
    height: var(--target-min);
    content: '';
    transform: translateY(-50%);
  }
}

.steps__files,
.steps__warnings {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin: 0;
  padding-left: var(--space-4);
}

.steps__files li {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.steps__tail-summary {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  min-height: var(--target-min);
  cursor: pointer;
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.steps__chevron {
  transition: transform var(--duration-fast) var(--ease-standard);
}

.steps__tail[open] .steps__chevron {
  transform: rotate(90deg);
}

@media (prefers-reduced-motion: reduce) {
  .steps__chevron {
    transition: none;
  }
}

.steps__tail-summary:focus-visible {
  border-radius: var(--radius-sm);
  outline: none;
  box-shadow: var(--focus-ring);
}

.steps__pre {
  max-height: 16rem;
  margin: 0;
  padding: var(--space-2) var(--space-3);
  overflow: auto;
  border-radius: var(--radius-md);
  background: var(--bg-surface-2);
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
