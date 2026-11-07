<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Consult, TaskSummary, Verdict } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Dialog from '@/components/ui/Dialog.vue'
import { isActive } from '@/lib/taskStatus'
import { useTeamStore } from '@/stores/team'

import { memberLabel } from './consults'
import type { TaskActions } from './useTaskActions'

export type TaskDialog =
  { kind: 'review'; verdict: Verdict } | { kind: 'cancel' } | { kind: 'takeover' } | null

/**
 * The confirmations of the task actions that change the work: review (with notes, which a rejection sends back
 * to the worker), cancel and take over. Each says what will happen before anything is sent. Accepting over a
 * peer objection quotes what each objecting peer requires, since the dialog covers the panel that says it.
 *
 * What was typed is a draft: closing the dialog (Esc, the backdrop, Cancel) keeps it for the same dialog on the
 * same task, and only a sent review, or another dialog, starts empty.
 */
const props = defineProps<{
  task: TaskSummary
  actions: TaskActions
  /** Peer objections that stand: accepting needs a written answer to them (sent as answerObjection). */
  objections?: readonly Consult[]
}>()
const dialog = defineModel<TaskDialog>({ required: true })

const { t } = useI18n()
const team = useTeamStore()
const id = useId()
const notes = ref('')
const answer = ref('')

const keyOf = (value: TaskDialog) =>
  value ? `${props.task.id}:${value.kind}:${value.kind === 'review' ? value.verdict : ''}` : null
let draftKey: string | null = null
watch(
  () => keyOf(dialog.value),
  (key) => {
    if (!key || key === draftKey) return
    draftKey = key
    notes.value = ''
    answer.value = ''
  },
)

const answering = computed(
  () =>
    !!props.objections?.length &&
    dialog.value?.kind === 'review' &&
    dialog.value.verdict === 'accepted',
)

const title = computed(() => {
  const value = dialog.value
  if (!value) return ''
  if (answering.value) return t('task.consults.answerTitle')
  if (value.kind === 'review') return t(`task.review.${value.verdict}Title`)
  return t(`task.confirm.${value.kind}Title`)
})

const description = computed(() => {
  const value = dialog.value
  if (!value) return ''
  if (answering.value) return t('task.consults.answerDesc')
  if (value.kind === 'review') return t(`task.review.${value.verdict}Desc`)
  if (value.kind === 'takeover') {
    return t(isActive(props.task) ? 'task.confirm.takeoverActiveDesc' : 'task.confirm.takeoverDesc')
  }
  return t('task.confirm.cancelDesc')
})

const busy = computed(() => props.actions.busy.value !== null)
const missingAnswer = computed(() => answering.value && !answer.value.trim())

async function confirm() {
  const value = dialog.value
  if (!value || missingAnswer.value) return
  const ok =
    value.kind === 'review'
      ? await props.actions.review(
          value.verdict,
          notes.value,
          answering.value ? answer.value : undefined,
        )
      : value.kind === 'cancel'
        ? await props.actions.cancel()
        : await props.actions.takeover()
  if (ok) {
    draftKey = null // sent: the next dialog starts empty
    dialog.value = null
  }
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) {
    event.preventDefault()
    void confirm()
  }
}
</script>

<template>
  <Dialog
    :open="dialog !== null"
    size="sm"
    :title="title"
    :description="description"
    :dismissible="!busy"
    :initial-focus="dialog?.kind === 'review' ? 'textarea' : undefined"
    @update:open="(open) => !open && (dialog = null)"
  >
    <div v-if="dialog?.kind === 'review'" class="td">
      <template v-if="answering">
        <ul class="td__objections">
          <li v-for="c in objections" :key="c.id">
            <strong>{{ memberLabel(team.members, c.member) }}</strong>
            <ul class="td__required">
              <li
                v-for="(r, i) in c.required.length ? c.required : c.findings.map((f) => f.text)"
                :key="i"
              >
                {{ r }}
              </li>
            </ul>
          </li>
        </ul>
        <label :for="`${id}-answer`" class="td__label">{{ t('task.consults.answerLabel') }}</label>
        <textarea
          :id="`${id}-answer`"
          v-model="answer"
          class="td__input"
          rows="3"
          required
          aria-required="true"
          @keydown="onKeydown"
        />
      </template>
      <label :for="`${id}-notes`" class="td__label">
        {{ t(dialog.verdict === 'rejected' ? 'task.review.notesReject' : 'task.review.notes') }}
      </label>
      <textarea
        :id="`${id}-notes`"
        v-model="notes"
        class="td__input"
        rows="4"
        :placeholder="t('task.review.notesPlaceholder')"
        @keydown="onKeydown"
      />
    </div>
    <template #footer>
      <Button variant="ghost" :disabled="busy" @click="dialog = null">
        {{ t('common.action.cancel') }}
      </Button>
      <Button
        :variant="
          dialog?.kind === 'cancel' || (dialog?.kind === 'review' && dialog.verdict === 'rejected')
            ? 'danger'
            : 'primary'
        "
        :loading="busy"
        :disabled="missingAnswer"
        @click="confirm"
      >
        {{
          dialog?.kind === 'review'
            ? t(`task.review.${dialog.verdict}Confirm`)
            : dialog
              ? t(`task.confirm.${dialog.kind}Confirm`)
              : ''
        }}
      </Button>
    </template>
  </Dialog>
</template>

<style scoped>
.td {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

/* What the objecting peers require, quoted (the dialog covers the opinions panel). */
.td__objections {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: 0;
  padding: var(--space-2) var(--space-3);
  border-left: 3px solid var(--border-strong);
  background: var(--bg-surface-2);
  font-size: var(--fs-sm);
  list-style: none;
}

.td__required {
  margin: var(--space-1) 0 0;
  padding-left: var(--space-5);
  overflow-wrap: anywhere;
}

.td__label {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.td__input {
  width: 100%;
  padding: var(--space-2) var(--space-3);
  border: var(--border-width) solid var(--border-strong);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
  color: var(--text-primary);
  font: inherit;
  font-size: var(--fs-sm);
  resize: vertical;
}

.td__input:focus-visible {
  outline: none;
  border-color: var(--border-focus);
  box-shadow: var(--focus-ring);
}
</style>
