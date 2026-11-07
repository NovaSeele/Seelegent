<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import type { GitFile } from '@/api/types'
import DiffView from '@/components/diff/DiffView.vue'
import Button from '@/components/ui/Button.vue'
import Spinner from '@/components/ui/Spinner.vue'
import Switch from '@/components/ui/Switch.vue'
import TextArea from '@/components/ui/TextArea.vue'

import NoteItem from './NoteItem.vue'
import type { DiffState } from './useGit'
import type { NotesModel } from './useNotes'

/**
 * The diff of the file selected in the Git view, with its path and the line-wrap switch. A new-side line number
 * starts a review note on that line; the file's notes show under their lines.
 */
const props = defineProps<{
  file: GitFile | null | undefined
  diff: DiffState
  notes: NotesModel
}>()
const emit = defineEmits<{ retry: [] }>()
const wrap = defineModel<boolean>('wrap', { required: true })

const { t } = useI18n()
const line = ref<number | null>(null)
const body = ref('')
const adding = ref(false)
const failure = ref('')

const fileNotes = computed(() =>
  props.file ? props.notes.notes.value.filter((n) => n.file === props.file?.path) : [],
)
watch(
  () => props.file?.path,
  () => (line.value = null),
)

function pick(n: number) {
  line.value = n
  failure.value = ''
}

async function add() {
  const path = props.file?.path
  if (!path || line.value === null || !body.value.trim()) return
  adding.value = true
  failure.value = ''
  try {
    await props.notes.add({ file: path, line: line.value, side: 'new', body: body.value.trim() })
    body.value = ''
    line.value = null
  } catch (error) {
    failure.value = errorMessage(error)
  } finally {
    adding.value = false
  }
}
</script>

<template>
  <section class="pane" :aria-label="t('git.diffLabel')">
    <div v-if="file" class="pane__bar">
      <code class="pane__path">
        {{ file.oldPath ? `${file.oldPath} → ` : '' }}{{ file.path }}
      </code>
      <Switch v-model="wrap" :label="t('git.wrap')" />
    </div>
    <p v-if="diff.status === 'loading'" class="pane__state">
      <Spinner size="sm" decorative />
      {{ t('git.diff.loading') }}
    </p>
    <div v-else-if="diff.status === 'error'" class="pane__state" role="alert">
      <span>{{ t('git.diff.error', { error: diff.error }) }}</span>
      <Button size="sm" @click="emit('retry')">{{ t('common.action.retry') }}</Button>
    </div>
    <form v-if="file && line !== null" class="pane__composer" @submit.prevent="add">
      <TextArea
        v-model="body"
        :label="t('team.notes.composer.title', { file: file.path, line })"
        :rows="3"
      />
      <p v-if="failure" class="pane__error" role="alert">{{ failure }}</p>
      <div class="pane__actions">
        <Button size="sm" variant="ghost" :disabled="adding" @click="line = null">
          {{ t('common.action.cancel') }}
        </Button>
        <Button
          type="submit"
          size="sm"
          variant="primary"
          :loading="adding"
          :disabled="!body.trim()"
        >
          {{ t('team.notes.composer.add') }}
        </Button>
      </div>
    </form>
    <DiffView
      v-if="diff.status === 'ready'"
      :diff="diff.diff"
      :wrap="wrap"
      annotatable
      :notes="fileNotes"
      @line="pick"
    >
      <template #note="{ note }">
        <NoteItem :note="note" :notes="notes" />
      </template>
    </DiffView>
  </section>
</template>

<style scoped>
.pane {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}

.pane__bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.pane__path {
  min-width: 0;
  font-family: var(--font-mono);
  font-size: var(--fs-sm);
  overflow-wrap: anywhere;
}

.pane__composer {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3);
  border: var(--border-width) solid var(--accent-border);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
}

.pane__actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
}

.pane__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
}

.pane__state {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-6) var(--space-4);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}
</style>
