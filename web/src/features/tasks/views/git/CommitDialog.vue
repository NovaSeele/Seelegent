<script setup lang="ts">
import { computed, ref, shallowRef, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { GitFile, TaskGit } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Checkbox from '@/components/ui/Checkbox.vue'
import Dialog from '@/components/ui/Dialog.vue'

import { committable } from './git'
import type { GitBusy, GitModel } from './useGit'

/**
 * Commit some of the task's files. Only the task's changed files are offered (the daemon refuses anything else),
 * all ticked at first; a rename commits its old path with it. "Suggest" asks the daemon for a message, which
 * fills an empty box when it arrives. A refusal (the task still running, a hook failing) shows here, once.
 */
const props = defineProps<{
  files: readonly GitFile[]
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
const id = useId()
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
watch(
  () => props.suggestion,
  (next, previous) => {
    if (
      next?.state === 'ready' &&
      previous?.state !== 'ready' &&
      next.message &&
      !message.value.trim()
    ) {
      message.value = next.message
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

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) {
    event.preventDefault()
    void submit()
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
      <div class="cd__field">
        <div class="cd__label-row">
          <label :for="`${id}-msg`" class="cd__label">{{ t('git.commit.message') }}</label>
          <Button
            v-if="!suggestion || suggestion.state === 'idle' || suggestion.state === 'running'"
            size="sm"
            variant="ghost"
            :loading="suggestion?.state === 'running' || busy === 'suggest'"
            @click="emit('suggest')"
          >
            {{ t('git.suggest.ask') }}
          </Button>
        </div>
        <textarea
          :id="`${id}-msg`"
          v-model="message"
          class="cd__input"
          rows="4"
          :placeholder="t('git.commit.placeholder')"
          @keydown="onKeydown"
        />
        <div
          v-if="suggestion?.state === 'ready' || suggestion?.state === 'failed'"
          class="cd__suggestion"
          :class="{ 'cd__suggestion--failed': suggestion.state === 'failed' }"
        >
          <span v-if="suggestion.state === 'failed'">{{ t('git.suggest.failed') }}</span>
          <code v-else class="cd__suggestion-text">{{ suggestion.message }}</code>
          <span class="cd__suggestion-actions">
            <Button
              v-if="suggestion.state === 'ready' && suggestion.message"
              size="sm"
              @click="message = suggestion.message ?? ''"
            >
              {{ t('git.suggest.use') }}
            </Button>
            <Button
              size="sm"
              variant="ghost"
              :loading="busy === 'dismiss'"
              @click="emit('dismiss')"
            >
              {{ t('git.suggest.dismiss') }}
            </Button>
          </span>
        </div>
      </div>

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

.cd__field {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.cd__label-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.cd__label {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  font-weight: var(--fw-medium);
}

.cd__input {
  width: 100%;
  padding: var(--space-2) var(--space-3);
  border: var(--border-width) solid var(--border-strong);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: var(--fs-sm);
  resize: vertical;
}

.cd__input:focus-visible {
  outline: none;
  border-color: var(--border-focus);
  box-shadow: var(--focus-ring);
}

.cd__suggestion {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border: var(--border-width) solid var(--accent-border);
  border-radius: var(--radius-md);
  background: var(--accent-soft);
  font-size: var(--fs-sm);
}

.cd__suggestion--failed {
  border-color: var(--status-danger-border);
  background: var(--status-danger-bg);
  color: var(--status-danger-fg);
}

.cd__suggestion-text {
  flex: 1;
  min-width: 0;
  font-family: var(--font-mono);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.cd__suggestion-actions {
  display: flex;
  gap: var(--space-1);
  margin-left: auto;
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
