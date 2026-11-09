<script setup lang="ts">
import { computed, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { GitFile, TaskGit } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Checkbox from '@/components/ui/Checkbox.vue'
import Dialog from '@/components/ui/Dialog.vue'

import CommitMessage from './CommitMessage.vue'
import { committable } from './git'
import type { GitBusy, GitModel } from './useGit'

/**
 * Commit some of the task's files. Only the task's changed files are offered (the daemon refuses anything else),
 * all ticked at first; a rename commits its old path with it. "Suggest" asks the daemon for a message, which
 * fills an empty box when it arrives. A refusal (the task still running, a hook failing) shows here, once.
 */
const props = defineProps<{
  files: readonly GitFile[]
  /** The task's files not offered: in untracked folders left closed in the list, or past an open one's list. */
  hidden?: number
  suggestion: TaskGit['suggestion']
  busy: GitBusy
  /** The task is still working: committing waits until it finishes. */
  active: boolean
  commit: GitModel['commit']
}>()
const emit = defineEmits<{ suggest: []; dismiss: [] }>()
const open = defineModel<boolean>('open', { required: true })
const message = defineModel<string>('message', { required: true })

const { t } = useI18n()
const chosen = shallowRef(new Set<string>())
const error = ref<string | null>(null)

watch(open, (value) => {
  if (!value) return
  chosen.value = new Set(props.files.map((f) => f.path))
  error.value = null
})
watch(
  () => props.files,
  (files) => {
    const paths = new Set(files.map((f) => f.path))
    if ([...chosen.value].some((p) => !paths.has(p))) {
      chosen.value = new Set([...chosen.value].filter((p) => paths.has(p)))
    }
  },
)
const paths = computed(() => committable(props.files, chosen.value))
const all = computed(() => props.files.length > 0 && paths.value.length === props.files.length)
const some = computed(() => paths.value.length > 0 && !all.value)
const hasRename = computed(() => props.files.some((f) => f.oldPath && chosen.value.has(f.path)))
const canCommit = computed(
  () => !props.active && !!message.value.trim() && paths.value.length > 0 && !props.busy,
)

function setAll(value: boolean) {
  chosen.value = new Set(value ? props.files.map((f) => f.path) : [])
}

function toggle(path: string, value: boolean) {
  const next = new Set(chosen.value)
  if (value) next.add(path)
  else next.delete(path)
  chosen.value = next
}

async function submit() {
  if (!canCommit.value) return
  error.value = null
  const outcome = await props.commit(message.value.trim(), paths.value)
  if (outcome.ok) {
    message.value = ''
    open.value = false
  } else {
    error.value = outcome.error
  }
}

const fileLabel = (file: GitFile) => (file.oldPath ? `${file.oldPath} → ${file.path}` : file.path)
</script>

<template>
  <Dialog
    v-model:open="open"
    size="lg"
    :title="t('git.commit.title')"
    :description="t('git.commit.desc')"
    :dismissible="busy !== 'commit'"
    initial-focus="textarea"
  >
    <div class="cd">
      <CommitMessage
        v-model="message"
        :suggestion="suggestion"
        :busy="busy"
        @suggest="emit('suggest')"
        @dismiss="emit('dismiss')"
        @submit="submit"
      />

      <fieldset class="cd__files">
        <legend class="cd__label">{{ t('git.commit.files') }}</legend>
        <Checkbox
          :model-value="all"
          :indeterminate="some"
          :label="t('git.commit.all', { n: files.length }, files.length)"
          @update:model-value="setAll"
        />
        <ul class="cd__list">
          <li v-for="file in files" :key="file.path">
            <Checkbox
              :model-value="chosen.has(file.path)"
              :label="fileLabel(file)"
              @update:model-value="toggle(file.path, $event)"
            />
          </li>
        </ul>
        <p v-if="hasRename" class="cd__hint">{{ t('git.commit.renameNote') }}</p>
        <p v-if="hidden" class="cd__hint">
          {{ t('git.commit.hiddenNote', { n: hidden }, hidden) }}
        </p>
      </fieldset>

      <p v-if="active" class="cd__hint cd__hint--warn">{{ t('git.commit.active') }}</p>
      <p v-if="error" class="cd__error" role="alert">{{ error }}</p>
    </div>
    <template #footer>
      <Button variant="ghost" :disabled="busy === 'commit'" @click="open = false">
        {{ t('common.action.cancel') }}
      </Button>
      <Button
        variant="primary"
        :disabled="!canCommit && busy !== 'commit'"
        :loading="busy === 'commit'"
        @click="submit"
      >
        {{ t('git.commit.confirm', { n: paths.length }, paths.length) }}
      </Button>
    </template>
  </Dialog>
</template>

<style scoped>
.cd {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.cd__label {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  font-weight: var(--fw-medium);
}

.cd__files {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}

.cd__list {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  max-height: 16rem;
  padding: var(--space-2) var(--space-3);
  overflow: auto;
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-md);
  list-style: none;
  font-family: var(--font-mono);
  overflow-wrap: anywhere;
}

.cd__hint {
  color: var(--text-muted);
  font-size: var(--fs-xs);
}

.cd__hint--warn {
  color: var(--attention-fg);
  font-size: var(--fs-sm);
}

.cd__error {
  padding: var(--space-2) var(--space-3);
  border: var(--border-width) solid var(--status-danger-border);
  border-radius: var(--radius-md);
  background: var(--status-danger-bg);
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
  white-space: pre-wrap;
}
</style>
