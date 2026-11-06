<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { TaskSummary } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Icon from '@/components/ui/Icon.vue'
import { useFormat } from '@/composables/useFormat'
import { useNow } from '@/composables/useNow'
import { canCancel, canTakeOver, isFinished } from '@/lib/taskStatus'

import { reasonKey } from './question'
import QuestionCard from './QuestionCard.vue'
import type { TaskActions } from './useTaskActions'

/**
 * Why the task waits and what you can do: the reason, who has to act (you or the lead), since when, the detail,
 * and the actions (dismiss, take over, cancel). A question gets its answer card; take-over shows "you are
 * driving". Shown only while there is something to say.
 */
const props = defineProps<{ task: TaskSummary; actions: TaskActions }>()
const emit = defineEmits<{ confirm: [action: 'cancel' | 'takeover'] }>()

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
const tone = computed(() =>
  attention.value?.state === 'background_wait' ? 'neutral' : 'attention',
)
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
      <p class="banner__title">
        <strong>{{ reason }}</strong>
        <span class="banner__who">
          ·
          {{ t(attention.who === 'user' ? 'task.attention.needsYou' : 'task.attention.needsLead') }}
        </span>
        <time class="banner__since" :datetime="attention.since">
          · {{ fmt.relative(attention.since, now) }}
        </time>
      </p>
      <QuestionCard v-if="question" :detail="attention.detail" :actions="actions" />
      <p v-else-if="attention.detail" class="banner__detail">{{ attention.detail }}</p>
      <div class="banner__actions">
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
        <Button v-if="canCancel(task)" size="sm" variant="ghost" @click="emit('confirm', 'cancel')">
          {{ t('task.actions.cancel') }}
        </Button>
      </div>
    </div>
  </section>
</template>

<style scoped>
.banner {
  display: flex;
  gap: var(--space-3);
  margin: 0 var(--space-5) var(--space-3);
  padding: var(--space-3) var(--space-4);
  border: var(--border-width) solid var(--tone-border);
  border-radius: var(--radius-lg);
  background: var(--tone-bg);
}

.banner__icon {
  flex: none;
  margin-top: 2px;
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

.banner__title {
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

.banner__actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  margin-left: calc(-1 * var(--space-2));
}

@media (width < 600px) {
  .banner {
    margin: 0 var(--space-4) var(--space-3);
    padding: var(--space-3);
  }
}
</style>
