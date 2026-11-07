<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import type { Note } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import IconButton from '@/components/ui/IconButton.vue'
import type { IconName } from '@/components/ui/icons'
import StatusPill from '@/components/ui/StatusPill.vue'
import type { Tone } from '@/components/ui/tone'

import type { NotesModel } from './useNotes'

/**
 * One review note: its status (computed by the daemon from the current file), who wrote it, the text, and resolve,
 * reopen or delete. With `where`, the file and line come first as a button that opens the file.
 */
const props = defineProps<{ note: Note; notes: NotesModel; where?: boolean }>()
const emit = defineEmits<{ open: [path: string] }>()

const { t } = useI18n()
const busy = ref(false)
const failure = ref('')

const LOOK: Record<Note['status'], { tone: Tone; icon: IconName }> = {
  draft: { tone: 'neutral', icon: 'statusQueued' },
  outdated: { tone: 'muted', icon: 'statusCancelled' },
  sent: { tone: 'running', icon: 'statusRunning' },
  addressed: { tone: 'success', icon: 'statusAccepted' },
  unaddressed: { tone: 'attention', icon: 'statusBlocked' },
  resolved: { tone: 'muted', icon: 'statusAccepted' },
}

async function run(action: () => Promise<void>) {
  busy.value = true
  failure.value = ''
  try {
    await action()
  } catch (error) {
    failure.value = errorMessage(error)
  } finally {
    busy.value = false
  }
}

const line = () => props.note.currentLine || props.note.line
</script>

<template>
  <div class="note">
    <div class="note__head">
      <button v-if="where" type="button" class="note__where" @click="emit('open', note.file)">
        {{
          note.currentLine === 0 && note.status !== 'draft'
            ? t('team.notes.lineGone', { file: note.file })
            : t('team.notes.where', { file: note.file, line: line() })
        }}
      </button>
      <StatusPill
        size="sm"
        :tone="LOOK[note.status].tone"
        :icon="LOOK[note.status].icon"
        :label="t(`team.notes.status.${note.status}`)"
      />
      <span class="note__by">{{ t(`team.notes.by.${note.by}`) }}</span>
      <span class="note__spacer" />
      <Button
        v-if="note.status !== 'resolved'"
        size="sm"
        variant="ghost"
        :disabled="busy"
        @click="run(() => notes.setState(note.id, 'resolved'))"
      >
        {{ t('team.notes.resolve') }}
      </Button>
      <Button
        v-else
        size="sm"
        variant="ghost"
        :disabled="busy"
        @click="run(() => notes.setState(note.id, 'open'))"
      >
        {{ t('team.notes.reopen') }}
      </Button>
      <IconButton
        icon="trash"
        size="sm"
        :label="t('team.notes.delete', { file: note.file, line: line() })"
        :disabled="busy"
        @click="run(() => notes.remove(note.id))"
      />
    </div>
    <p class="note__body">{{ note.body }}</p>
    <p v-if="failure" class="note__error" role="alert">{{ failure }}</p>
  </div>
</template>

<style scoped>
.note {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  font-size: var(--fs-sm);
}

.note__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.note__where {
  min-height: var(--target-min);
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--link);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  text-align: left;
  overflow-wrap: anywhere;
  cursor: pointer;
}

.note__where:hover {
  text-decoration: underline;
}

.note__where:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}

.note__by {
  color: var(--text-muted);
  font-size: var(--fs-xs);
}

.note__spacer {
  flex: 1;
}

.note__body {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.note__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-xs);
}
</style>
