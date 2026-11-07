<script setup lang="ts">
import { computed, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { TaskGit } from '@/api/types'
import Button from '@/components/ui/Button.vue'

import type { GitBusy } from './useGit'

/**
 * The commit message box of the commit dialog, with the daemon's suggestion: asking for one, using it, or
 * dismissing it. A suggestion that arrives while the box is empty fills it. Ctrl/Cmd+Enter submits.
 */
const props = defineProps<{ suggestion: TaskGit['suggestion']; busy: GitBusy }>()
const emit = defineEmits<{ suggest: []; dismiss: []; submit: [] }>()
const message = defineModel<string>({ required: true })

const { t } = useI18n()
const id = useId()

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

/** The suggestion row, unless the message already says exactly what the suggestion says. */
const showSuggestion = computed(() => {
  const s = props.suggestion
  if (s?.state === 'failed') return true
  return s?.state === 'ready' && (s.message ?? '').trim() !== message.value.trim()
})

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) {
    event.preventDefault()
    emit('submit')
  }
}
</script>

<template>
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
      v-if="suggestion && showSuggestion"
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
        <Button size="sm" variant="ghost" :loading="busy === 'dismiss'" @click="emit('dismiss')">
          {{ t('git.suggest.dismiss') }}
        </Button>
      </span>
    </div>
  </div>
</template>

<style scoped>
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
</style>
