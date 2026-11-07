<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'

import type { TaskDetail, TaskSummary } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Icon from '@/components/ui/Icon.vue'
import { useFormat } from '@/composables/useFormat'
import { useNow } from '@/composables/useNow'
import { canCancel, canTakeOver, isFinished } from '@/lib/taskStatus'
import { useTeamStore } from '@/stores/team'

import { memberLabel, openObjections } from './consults'
import { failedSteps, gateOf } from './gate'
import { reasonKey } from './question'
import QuestionCard from './QuestionCard.vue'
import type { TaskActions } from './useTaskActions'

/**
 * Why the task waits and what you can do: the reason, who has to act (you or the lead), since when, the detail,
 * and the actions (dismiss, take over, cancel). A question gets its answer card; take-over shows "you are
 * driving". Shown only while there is something to say.
 */
const props = defineProps<{
  task: TaskSummary
  actions: TaskActions
  /** The open task's detail: a failed gate is told from its steps, never from the daemon's English detail. */
  detail?: TaskDetail | null
}>()
const emit = defineEmits<{ confirm: [action: 'cancel' | 'takeover']; answer: [] }>()

const { t, te } = useI18n()
const fmt = useFormat()
const now = useNow(30_000)

const attention = computed(() => {
  const value = props.task.attention
  return value && value.state !== 'info' ? value : null
})
const question = computed(() => attention.value?.state === 'question' && !isFinished(props.task))
const reason = computed(() => {
  const value = attention.value
  if (!value) return ''
  const key = `common.attention.${reasonKey(value.reason)}`
  return te(key) ? t(key) : value.reason
})
/** For a red gate: its failing steps, named by kind in the reader's language (null for other reasons). */
const gateSteps = computed(() =>
  attention.value?.reason === 'gate_red'
    ? failedSteps(gateOf(props.task, props.detail ?? null).result)
    : null,
)
const tone = computed(() =>
  attention.value?.state === 'background_wait' ? 'neutral' : 'attention',
)

/** The peer objections that stand, named after their members ("Peri Okafor objects; answer before accepting"). */
const team = useTeamStore()
const objections = computed(() =>
  attention.value?.reason === 'peer_objection' ? openObjections(props.detail) : [],
)
const objectionTitle = computed(() =>
  t(
    'task.consults.objects',
    { who: objections.value.map((c) => memberLabel(team.members, c.member)).join(', ') },
    objections.value.length,
  ),
)
onMounted(() => team.ensureMembers())
</script>

<template>
  <section v-if="task.manual" class="banner tone-attention" role="status">
    <Icon name="hand" class="banner__icon" />
    <div class="banner__body">
      <p class="banner__title">
        {{ t('task.attention.driving', { account: task.accountLabel ?? '' }) }}
      </p>
      <p class="banner__detail">{{ t('task.attention.drivingDesc') }}</p>
    </div>
  </section>
  <section v-else-if="attention" class="banner" :class="`tone-${tone}`" role="status">
    <Icon :name="question ? 'question' : 'statusBlocked'" class="banner__icon" />
    <div class="banner__body">
      <div class="banner__head">
        <p class="banner__title">
          <strong>{{ objections.length ? objectionTitle : reason }}</strong>
          <span class="banner__who">
            ·
            {{
              t(attention.who === 'user' ? 'task.attention.needsYou' : 'task.attention.needsLead')
            }}
          </span>
          <time class="banner__since" :datetime="attention.since">
            · {{ fmt.relative(attention.since, now) }}
          </time>
        </p>
        <div class="banner__actions">
          <Button v-if="objections.length" size="sm" variant="primary" @click="emit('answer')">
            {{ t('task.consults.answerAction') }}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            :loading="actions.busy.value === 'dismiss'"
            @click="actions.dismiss()"
          >
            {{ t('task.attention.dismiss') }}
          </Button>
          <Button
            v-if="canTakeOver(task)"
            size="sm"
            variant="ghost"
            @click="emit('confirm', 'takeover')"
          >
            {{ t('task.actions.takeover') }}
          </Button>
          <Button
            v-if="canCancel(task)"
            size="sm"
            variant="ghost"
            @click="emit('confirm', 'cancel')"
          >
            {{ t('task.actions.cancel') }}
          </Button>
        </div>
      </div>
      <QuestionCard v-if="question" :detail="attention.detail" :actions="actions" />
      <ul v-else-if="gateSteps" class="banner__detail banner__steps">
        <li v-for="(s, i) in gateSteps" :key="i">
          {{ t(s.kindKey)
          }}<template v-if="s.what"
            >: <code>{{ s.what }}</code></template
          ><span v-if="s.causedBy" class="banner__cause">
            · {{ t('task.gate.causedBy', { tasks: s.causedBy }) }}</span
          >
        </li>
      </ul>
      <ul v-else-if="objections.length" class="banner__detail banner__steps">
        <li v-for="c in objections" :key="c.id">
          {{
            t('task.consults.line', {
              who: memberLabel(team.members, c.member),
              text: c.required[0] ?? c.findings[0]?.text ?? t('task.consults.verdict.object'),
            })
          }}
        </li>
      </ul>
      <p v-else-if="attention.detail" class="banner__detail">{{ attention.detail }}</p>
    </div>
  </section>
</template>

<style scoped>
.banner {
  display: flex;
  gap: var(--space-3);
  margin: 0 var(--space-5) var(--space-3);
  padding: var(--space-2) var(--space-4) var(--space-3);
  border: var(--border-width) solid var(--tone-border);
  border-radius: var(--radius-lg);
  background: var(--tone-bg);
}

.banner__icon {
  flex: none;
  margin-top: var(--space-2);
  color: var(--tone-fg);
  font-size: var(--icon-lg);
}

.banner__body {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}

.banner__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-1) var(--space-3);
}

.banner__title {
  min-width: 0;
  color: var(--text-primary);
  font-size: var(--fs-sm);
}

.banner__title strong {
  color: var(--tone-fg);
}

.banner__who,
.banner__since {
  color: var(--text-secondary);
}

.banner__detail {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.banner__steps {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin: 0;
  padding: 0;
  list-style: none;
}

.banner__steps code {
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
}

.banner__cause {
  color: var(--text-muted);
}

.banner__actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  margin-right: calc(-1 * var(--space-2));
  margin-left: auto;
}

@media (width < 600px) {
  .banner {
    margin: 0 var(--space-4) var(--space-3);
    padding: var(--space-3);
  }
}
</style>
