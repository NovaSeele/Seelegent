<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { TaskDetail } from '@/api/types'
import Icon from '@/components/ui/Icon.vue'
import Spinner from '@/components/ui/Spinner.vue'
import StatusPill from '@/components/ui/StatusPill.vue'
import { useFormat } from '@/composables/useFormat'
import ClaimList from '@/features/meetings/ClaimList.vue'
import { useTeamStore } from '@/stores/team'

import { consultState, consultTone, isOpenObjection, memberLabel, openObjections } from './consults'

/**
 * Member opinions: the consults asked on the task (POST /tasks/{id}/consult), oldest first. Each shows the member
 * and rank, the verdict, the findings with their citation checks and what the member requires. A peer objection
 * the lead has not answered blocks accepting; the panel opens by itself while one stands. Hidden without consults.
 */
const props = defineProps<{ detail: TaskDetail | null }>()

const { t } = useI18n()
const fmt = useFormat()
const team = useTeamStore()
onMounted(() => team.ensureMembers())

const consults = computed(() => props.detail?.consults ?? [])
const open = computed(() => openObjections(props.detail).length)
const running = computed(() => consults.value.filter((c) => c.status === 'running').length)
const tone = computed(() => (open.value ? 'attention' : running.value ? 'running' : 'neutral'))

const expanded = ref(open.value > 0)
watch(open, (value) => {
  if (value) expanded.value = true
})
</script>

<template>
  <details
    v-if="consults.length"
    class="cp"
    :class="`tone-${tone}`"
    :open="expanded"
    @toggle="expanded = ($event.target as HTMLDetailsElement).open"
  >
    <summary class="cp__summary">
      <Spinner v-if="running && !open" size="sm" decorative />
      <Icon v-else :name="open ? 'warning' : 'info'" class="cp__icon" />
      <strong>{{ t('task.consults.title') }}</strong>
      <span class="cp__count">
        <template v-if="open">{{ t('task.consults.open', { count: open }, open) }}</template>
        <template v-else>{{
          t('task.consults.count', { count: consults.length }, consults.length)
        }}</template>
      </span>
    </summary>
    <ol class="cp__list">
      <li v-for="c in consults" :key="c.id" class="cp__item">
        <div class="cp__head">
          <strong class="cp__who">{{ memberLabel(team.members, c.member) }}</strong>
          <span class="cp__rank" :title="t(`team.rankHint.${c.rank}`)">{{
            t(`team.rank.${c.rank}`)
          }}</span>
          <StatusPill
            size="sm"
            :tone="consultTone(c)"
            :label="t(`task.consults.verdict.${consultState(c)}`)"
            :icon="isOpenObjection(c) ? 'warning' : undefined"
          />
          <time class="cp__at" :datetime="c.endedAt ?? c.at">{{
            fmt.dateTime(c.endedAt ?? c.at)
          }}</time>
        </div>
        <p v-if="c.focus" class="cp__muted">{{ t('task.consults.focus', { focus: c.focus }) }}</p>
        <p v-if="c.status === 'failed' && c.error" class="cp__error">{{ c.error }}</p>
        <ClaimList v-if="c.findings.length" :claims="c.findings" />
        <div v-if="c.required.length" class="cp__required">
          <p class="cp__label">{{ t('task.consults.required') }}</p>
          <ul>
            <li v-for="(r, i) in c.required" :key="i">{{ r }}</li>
          </ul>
        </div>
        <p v-if="c.answeredObjection" class="cp__answer">
          <span class="cp__label">{{ t('task.consults.answer') }}</span>
          {{ c.answeredObjection.text }}
        </p>
      </li>
    </ol>
  </details>
</template>

<style scoped>
.cp {
  margin: 0 var(--space-5) var(--space-3);
  border: var(--border-width) solid var(--tone-border);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
}

.cp__summary {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) var(--space-2);
  min-height: var(--target-min);
  padding: var(--space-2) var(--space-4);
  cursor: pointer;
  font-size: var(--fs-sm);
}

.cp__summary:focus-visible {
  border-radius: var(--radius-lg);
  outline: none;
  box-shadow: var(--focus-ring);
}

.cp__icon {
  color: var(--tone-fg);
}

.cp__count {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.cp__list {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  max-height: 45vh;
  margin: 0;
  padding: 0 var(--space-4) var(--space-3);
  overflow-y: auto;
  list-style: none;
}

.cp__item {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
  font-size: var(--fs-sm);
}

.cp__item + .cp__item {
  padding-top: var(--space-3);
  border-top: var(--border-width) solid var(--border-subtle);
}

.cp__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) var(--space-2);
}

.cp__rank,
.cp__at,
.cp__muted,
.cp__label {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.cp__at {
  margin-left: auto;
}

.cp__error {
  color: var(--status-danger-fg);
  overflow-wrap: anywhere;
}

.cp__required ul {
  margin: var(--space-1) 0 0;
  padding-left: var(--space-5);
}

.cp__required li,
.cp__answer {
  overflow-wrap: anywhere;
}

.cp__answer .cp__label {
  margin-right: var(--space-1);
}

@media (height < 760px) {
  .cp__list {
    max-height: 30vh;
  }
}

@media (width < 600px) {
  .cp {
    margin: 0 var(--space-4) var(--space-3);
  }

  .cp__list {
    max-height: none;
  }
}
</style>
