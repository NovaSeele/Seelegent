<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import Button from '@/components/ui/Button.vue'
import TextArea from '@/components/ui/TextArea.vue'
import { useToastStore } from '@/stores/toast'

import NoteItem from './NoteItem.vue'
import type { NotesModel } from './useNotes'

/**
 * Every review note of the task, by file and line, and the send: the drafts and the notes the last turn left
 * unaddressed go to the worker as one followup, with an optional message after them. A running turn cannot take
 * them yet.
 */
const props = defineProps<{ notes: NotesModel; busy: boolean }>()
const emit = defineEmits<{ open: [path: string] }>()

const { t } = useI18n()
const toast = useToastStore()
const extra = ref('')
const sending = ref(false)
const failure = ref('')

const list = computed(() =>
  [...props.notes.notes.value].sort(
    (a, b) => a.file.localeCompare(b.file) || (a.currentLine || a.line) - (b.currentLine || b.line),
  ),
)
const count = computed(() => props.notes.toSend.value.length)

async function send() {
  sending.value = true
  failure.value = ''
  try {
    const n = await props.notes.send(extra.value)
    extra.value = ''
    toast.show({ message: t('team.notes.sent', { n }, n), tone: 'success' })
  } catch (error) {
    failure.value = errorMessage(error)
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <section class="notes" :aria-label="t('team.notes.title')">
    <h3 class="notes__title">
      {{ t('team.notes.title') }}
      <span class="notes__count">{{
        t('team.notes.countN', { n: list.length }, list.length)
      }}</span>
    </h3>
    <p v-if="notes.status.value === 'error'" class="notes__error" role="alert">
      {{ t('team.notes.loadFailed', { error: notes.error.value }) }}
    </p>
    <p v-else-if="!list.length" class="notes__muted">{{ t('team.notes.empty') }}</p>
    <ul v-else class="notes__list">
      <li v-for="n in list" :key="n.id">
        <NoteItem :note="n" :notes="notes" where @open="emit('open', $event)" />
      </li>
    </ul>
    <form v-if="list.length" class="notes__send" @submit.prevent="send">
      <TextArea v-model="extra" :label="t('team.notes.extra')" :rows="2" />
      <p v-if="busy" class="notes__muted">{{ t('team.notes.busy') }}</p>
      <p v-if="failure" class="notes__error" role="alert">{{ failure }}</p>
      <Button
        type="submit"
        variant="primary"
        icon="send"
        :loading="sending"
        :disabled="!count || busy"
      >
        {{ t('team.notes.send', { n: count }, count) }}
      </Button>
    </form>
  </section>
</template>

<style scoped>
.notes {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-4);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
}

.notes__title {
  display: flex;
  align-items: baseline;
  gap: var(--space-2);
  font-size: var(--fs-sm);
  font-weight: var(--fw-semibold);
}

.notes__count,
.notes__muted {
  color: var(--text-muted);
  font-size: var(--fs-xs);
  font-weight: var(--fw-regular);
}

.notes__list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.notes__list li + li {
  padding-top: var(--space-2);
  border-top: var(--border-width) solid var(--border-subtle);
}

.notes__send {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--space-2);
}

.notes__send > :first-child {
  align-self: stretch;
}

.notes__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
}
</style>
