<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import RichText from '@/components/RichText.vue'
import Button from '@/components/ui/Button.vue'
import Kbd from '@/components/ui/Kbd.vue'

import { parseQuestion } from './question'
import type { TaskActions } from './useTaskActions'

/**
 * The worker's question with its numbered choices and a free answer. While focus is in the card (outside the
 * text box), 1-9 send that choice at once; Mod+Enter sends the typed answer.
 */
const props = defineProps<{ detail?: string; actions: TaskActions }>()

const { t } = useI18n()
const id = useId()
const parsed = computed(() => parseQuestion(props.detail))
const answer = ref('')
const sending = computed(() => props.actions.busy.value === 'answer')

async function send(text: string) {
  const value = text.trim()
  if (!value || sending.value) return
  if (await props.actions.answer(value)) answer.value = ''
}

function onKeydown(event: KeyboardEvent) {
  const inField = event.target instanceof HTMLTextAreaElement
  if (inField) {
    if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) {
      event.preventDefault()
      void send(answer.value)
    }
    return
  }
  if (event.ctrlKey || event.metaKey || event.altKey) return
  const option = parsed.value.options.find((o) => String(o.n) === event.key)
  if (!option) return
  // The card owns digits while focused: they choose, not switch the task view.
  event.preventDefault()
  event.stopPropagation()
  void send(option.label)
}
</script>

<template>
  <section class="qc" :aria-labelledby="`${id}-title`" @keydown="onKeydown">
    <h3 :id="`${id}-title`" class="qc__title">{{ t('task.question.title') }}</h3>
    <RichText v-if="parsed.text" :text="parsed.text" class="qc__text" />
    <div v-if="parsed.options.length" class="qc__options">
      <Button
        v-for="option in parsed.options"
        :key="option.n"
        size="sm"
        :disabled="sending"
        :aria-keyshortcuts="option.n <= 9 ? String(option.n) : undefined"
        class="qc__option"
        @click="send(option.label)"
      >
        <Kbd v-if="option.n <= 9" :keys="String(option.n)" size="sm" />
        {{ option.label }}
      </Button>
    </div>
    <label :for="`${id}-answer`" class="qc__label">{{ t('task.question.own') }}</label>
    <textarea
      :id="`${id}-answer`"
      v-model="answer"
      class="qc__input"
      rows="2"
      :placeholder="t('task.question.placeholder')"
      :disabled="sending"
    />
    <div class="qc__foot">
      <span class="qc__hint">{{ t('task.question.hint') }}</span>
      <Button
        size="sm"
        variant="primary"
        icon="send"
        :loading="sending"
        :disabled="!answer.trim()"
        @click="send(answer)"
      >
        {{ t('task.question.send') }}
      </Button>
    </div>
  </section>
</template>

<style scoped>
.qc {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.qc__title {
  font-size: var(--fs-sm);
  font-weight: var(--fw-semibold);
}

.qc__text {
  font-size: var(--fs-sm);
}

.qc__options {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.qc__option {
  max-width: 100%;
  white-space: normal;
  text-align: left;
}

.qc__label {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.qc__input {
  width: 100%;
  min-height: 56px;
  padding: var(--space-2);
  border: var(--border-width) solid var(--border-strong);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
  color: var(--text-primary);
  font: inherit;
  font-size: var(--fs-sm);
  resize: vertical;
}

.qc__input:focus-visible {
  outline: none;
  border-color: var(--border-focus);
  box-shadow: var(--focus-ring);
}

.qc__foot {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.qc__hint {
  color: var(--text-muted);
  font-size: var(--fs-2xs);
}
</style>
