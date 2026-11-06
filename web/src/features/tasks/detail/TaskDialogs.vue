<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { TaskSummary, Verdict } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Dialog from '@/components/ui/Dialog.vue'
import { isActive } from '@/lib/taskStatus'

import type { TaskActions } from './useTaskActions'

export type TaskDialog =
  { kind: 'review'; verdict: Verdict } | { kind: 'cancel' } | { kind: 'takeover' } | null

/**
 * The confirmations of the task actions that change the work: review (with notes, which a rejection sends back
 * to the worker), cancel and take over. Each says what will happen before anything is sent.
 */
const props = defineProps<{ task: TaskSummary; actions: TaskActions }>()
const dialog = defineModel<TaskDialog>({ required: true })

const { t } = useI18n()
const id = useId()
const notes = ref('')

watch(
  () => dialog.value?.kind,
  () => (notes.value = ''),
)

const title = computed(() => {
  const value = dialog.value
  if (!value) return ''
  if (value.kind === 'review') return t(`task.review.${value.verdict}Title`)
  return t(`task.confirm.${value.kind}Title`)
})

const description = computed(() => {
  const value = dialog.value
  if (!value) return ''
  if (value.kind === 'review') return t(`task.review.${value.verdict}Desc`)
  if (value.kind === 'takeover') {
    return t(isActive(props.task) ? 'task.confirm.takeoverActiveDesc' : 'task.confirm.takeoverDesc')
  }
  return t('task.confirm.cancelDesc')
})

const busy = computed(() => props.actions.busy.value !== null)

async function confirm() {
  const value = dialog.value
  if (!value) return
  const ok =
    value.kind === 'review'
      ? await props.actions.review(value.verdict, notes.value)
      : value.kind === 'cancel'
        ? await props.actions.cancel()
        : await props.actions.takeover()
  if (ok) dialog.value = null
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
