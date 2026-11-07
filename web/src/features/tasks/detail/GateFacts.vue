<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import type { TaskProof, VerifierResult } from '@/api/types'
import Chip from '@/components/ui/Chip.vue'
import type { Tone } from '@/components/ui/tone'

/**
 * What the task's gate checks (lane, commands, doctrine, the frozen bar) and the second-opinion review of its
 * last green gate. Reasons and paths are worker and lead text: shown as text only.
 */
const props = defineProps<{ proof: TaskProof | null; verifier: VerifierResult | null }>()

const { t } = useI18n()

const verdictTone = computed<Tone>(() => {
  switch (props.verifier?.verdict) {
    case 'accept':
      return 'success'
    case 'reject':
      return 'danger'
    case 'running':
      return 'running'
    default:
      return 'attention'
  }
})
const unverified = computed(
  () => props.verifier?.cites.filter((c) => c.state !== 'verified' && c.state !== 'located') ?? [],
)
</script>

<template>
  <section v-if="verifier" class="facts">
    <h3 class="facts__title">
      {{ t('task.gate.verifier.title') }}
      <Chip :tone="verdictTone">{{ t(`task.gate.verifier.verdict.${verifier.verdict}`) }}</Chip>
    </h3>
    <ul v-if="verifier.reasons.length" class="facts__list facts__list--bullets">
      <li v-for="(reason, i) in verifier.reasons" :key="i">{{ reason }}</li>
    </ul>
    <p v-if="unverified.length" class="facts__muted">
      {{ t('task.gate.verifier.unverified', { n: unverified.length }) }}
      <code v-for="c in unverified" :key="`${c.path}:${c.line}`" class="facts__code">
        {{ c.path }}:{{ c.line }}
      </code>
    </p>
    <p v-if="verifier.detail" class="facts__muted">{{ verifier.detail }}</p>
  </section>
  <section v-if="proof" class="facts">
    <h3 class="facts__title">{{ t('task.gate.checks') }}</h3>
    <dl class="facts__grid">
      <dt>{{ t('task.gate.lane.title') }}</dt>
      <dd>
        <template v-if="proof.lane">
          <code v-for="p in proof.lane.paths" :key="p" class="facts__code">{{ p }}</code>
          <span class="facts__muted">{{ t(`task.gate.lane.source.${proof.lane.source}`) }}</span>
          <span v-if="proof.lane.forced?.length" class="facts__muted">
            {{ t('task.gate.lane.forced', { tasks: proof.lane.forced.join(', ') }) }}
          </span>
        </template>
        <span v-else class="facts__muted">{{ t('task.gate.lane.none') }}</span>
      </dd>
      <dt>{{ t('task.gate.commands') }}</dt>
      <dd>
        <span v-if="!proof.gate" class="facts__muted">{{ t('task.gate.off') }}</span>
        <span v-else-if="!proof.commands.length" class="facts__muted">
          {{ t('task.gate.noCommands') }}
        </span>
        <ul v-else class="facts__list">
          <li v-for="(c, i) in proof.commands" :key="i">
            <code class="facts__code">{{ c.dir ? `cd ${c.dir} && ${c.cmd}` : c.cmd }}</code>
            <span v-if="c.kind === 'detect'" class="facts__muted">{{
              t('task.gate.detected')
            }}</span>
            <span v-if="c.taskIds?.length" class="facts__muted">{{ c.taskIds.join(', ') }}</span>
          </li>
        </ul>
      </dd>
      <template v-if="proof.doctrine.length || proof.doctrineError">
        <dt>{{ t('task.gate.doctrine') }}</dt>
        <dd>
          <code v-for="id in proof.doctrine" :key="id" class="facts__code">{{ id }}</code>
          <span v-if="proof.doctrineError" class="facts__danger">{{ proof.doctrineError }}</span>
        </dd>
      </template>
      <dt>{{ t('task.gate.budget') }}</dt>
      <dd>{{ t('task.gate.fixes', { n: proof.autoFixes, max: proof.budget }) }}</dd>
      <dt>{{ t('task.gate.verifier.title') }}</dt>
      <dd>{{ proof.verify ? t('task.gate.verifier.on') : t('task.gate.verifier.off') }}</dd>
      <template v-if="proof.feature">
        <dt>{{ t('task.gate.feature') }}</dt>
        <dd>
          <RouterLink :to="{ name: 'proof' }" class="facts__link">{{ proof.feature }}</RouterLink>
        </dd>
      </template>
      <template v-if="proof.barChanged.length">
        <dt>{{ t('task.gate.bar') }}</dt>
        <dd class="facts__danger">
          <code v-for="b in proof.barChanged" :key="b" class="facts__code">{{ b }}</code>
        </dd>
      </template>
    </dl>
  </section>
</template>

<style scoped>
.facts {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  font-size: var(--fs-sm);
}

.facts__title {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--fs-sm);
  font-weight: var(--fw-semibold);
}

.facts__grid {
  display: grid;
  grid-template-columns: minmax(7rem, max-content) 1fr;
  gap: var(--space-1) var(--space-3);
  margin: 0;
}

.facts__grid dt {
  color: var(--text-secondary);
}

.facts__grid dd {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) var(--space-2);
  min-width: 0;
  margin: 0;
}

.facts__list {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin: 0;
  padding: 0;
  list-style: none;
}

.facts__list--bullets {
  padding-left: var(--space-4);
  list-style: disc;
  overflow-wrap: anywhere;
}

.facts__code {
  min-width: 0;
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  overflow-wrap: anywhere;
}

.facts__muted {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  overflow-wrap: anywhere;
}

.facts__danger {
  color: var(--status-danger-fg);
  overflow-wrap: anywhere;
}

.facts__link {
  color: var(--accent);
}

@media (width < 600px) {
  .facts__grid {
    grid-template-columns: 1fr;
  }

  .facts__grid dd {
    margin-bottom: var(--space-1);
  }
}

@media (pointer: coarse) {
  .facts__link {
    display: inline-flex;
    align-items: center;
    min-height: var(--target-min);
  }
}
</style>
