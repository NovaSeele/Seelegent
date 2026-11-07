<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type { ProofRun } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Chip from '@/components/ui/Chip.vue'
import { useFormat } from '@/composables/useFormat'

import { runTone } from './proofModel'

/**
 * The latest regression runs, newest first, live over proof.run. Each run lists its commands with their state and
 * the end of their output; a running one can be stopped.
 */
defineProps<{ runs: readonly ProofRun[]; busy: boolean }>()
const emit = defineEmits<{ cancel: [id: string] }>()

const { t } = useI18n()
const fmt = useFormat()
</script>

<template>
  <section class="runs" :aria-label="t('proof.runs.title')">
    <h2 class="runs__title">{{ t('proof.runs.title') }}</h2>
    <p v-if="!runs.length" class="runs__muted">{{ t('proof.runs.empty') }}</p>
    <ul v-else class="runs__list">
      <li v-for="run in runs" :key="run.id" class="runs__item">
        <details :open="run.state === 'running' || run.state === 'red'">
          <summary class="runs__summary">
            <Chip :tone="runTone(run.state)">{{ t(`proof.runs.state.${run.state}`) }}</Chip>
            <span class="runs__what">{{ run.feature || t('proof.runs.all') }}</span>
            <span class="runs__muted">
              {{
                t('proof.runs.counts', {
                  green: run.counts.green,
                  red: run.counts.red,
                  error: run.counts.error,
                  total: run.total,
                })
              }}
            </span>
            <time class="runs__muted" :datetime="run.startedAt">{{
              fmt.dateTime(run.startedAt)
            }}</time>
          </summary>
          <div class="runs__body">
            <p v-if="run.noisy" class="runs__note">{{ t('proof.runs.noisy') }}</p>
            <p v-if="run.dirty" class="runs__note">{{ t('proof.runs.dirty') }}</p>
            <p v-if="run.error" class="runs__error">{{ run.error }}</p>
            <ul class="runs__results">
              <li v-for="(r, i) in run.results" :key="i" class="runs__result">
                <div class="runs__row">
                  <Chip :tone="runTone(r.state === 'running' ? 'running' : r.state)">
                    {{ t(`proof.runs.state.${r.state}`) }}
                  </Chip>
                  <code class="runs__cmd">{{ r.cmd }}</code>
                  <span class="runs__muted">{{ t(`proof.runs.kind.${r.kind}`) }}</span>
                  <span v-if="r.exit !== undefined" class="runs__muted">
                    {{ t('task.gate.exit', { n: r.exit }) }}
                  </span>
                  <span v-if="r.ms" class="runs__muted">{{ fmt.duration(r.ms) }}</span>
                </div>
                <pre v-if="r.tail && r.state !== 'green'" class="runs__pre">{{ r.tail }}</pre>
              </li>
            </ul>
            <Button
              v-if="run.state === 'running'"
              size="sm"
              variant="secondary"
              :loading="busy"
              @click="emit('cancel', run.id)"
            >
              {{ t('proof.runs.cancel') }}
            </Button>
          </div>
        </details>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.runs {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}

.runs__title {
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
}

.runs__list,
.runs__results {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.runs__item {
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
}

.runs__summary {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) var(--space-2);
  min-height: var(--target-min);
  padding: var(--space-2) var(--space-3);
  cursor: pointer;
  font-size: var(--fs-sm);
}

.runs__summary:focus-visible {
  border-radius: var(--radius-md);
  outline: none;
  box-shadow: var(--focus-ring);
}

.runs__what {
  font-weight: var(--fw-medium);
  overflow-wrap: anywhere;
}

.runs__body {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--space-2);
  padding: 0 var(--space-3) var(--space-3);
}

.runs__row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) var(--space-2);
  font-size: var(--fs-sm);
}

.runs__cmd {
  min-width: 0;
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  overflow-wrap: anywhere;
}

.runs__muted,
.runs__note {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.runs__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
}

.runs__results {
  width: 100%;
}

.runs__pre {
  max-height: 12rem;
  margin: var(--space-1) 0 0;
  padding: var(--space-2) var(--space-3);
  overflow: auto;
  border-radius: var(--radius-md);
  background: var(--bg-surface-2);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
