<script setup lang="ts">
import { computed, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { tasksApi } from '@/api'
import Icon from '@/components/ui/Icon.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useFormat } from '@/composables/useFormat'

import type { TimelineItem } from './timeline'

/**
 * The brief Seelegent sent the worker. Events carry its first 500 characters; "Show the whole brief" reads it
 * once from GET /tasks/{id}/prompt and keeps it in `briefs` (owned by the transcript, so scrolling the item out
 * of the window and back does not read it again).
 */
const props = defineProps<{
  item: Extract<TimelineItem, { kind: 'prompt' }>
  taskId: string
  expanded: boolean
  briefs: Map<string, string>
}>()
const emit = defineEmits<{ toggle: [] }>()

const { t } = useI18n()
const format = useFormat()

const full = shallowRef(props.briefs.get(props.item.key) ?? '')
const loading = shallowRef(false)
const error = shallowRef('')
const truncated = computed(() => props.item.length > props.item.preview.length)
const text = computed(() => (props.expanded && full.value ? full.value : props.item.preview))
const bodyId = computed(() => `brief-${props.item.key}`)

async function load() {
  if (full.value || loading.value || props.item.turn === undefined) return
  loading.value = true
  error.value = ''
  try {
    const result = await tasksApi.prompt(props.taskId, props.item.turn, props.item.attempt)
    full.value = result.prompt
    props.briefs.set(props.item.key, result.prompt)
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    loading.value = false
  }
}

watch(
  () => props.expanded,
  (open) => open && void load(),
  { immediate: true },
)
</script>

<template>
  <div class="brief">
    <button
      type="button"
      class="brief__head"
      :aria-expanded="expanded"
      :aria-controls="bodyId"
      @click="emit('toggle')"
    >
      <Icon name="file" class="brief__icon" />
      <span class="brief__title">{{ t('terminal.prompt.title') }}</span>
      <span class="brief__size">
        {{ t('terminal.prompt.size', { n: format.number(item.length) }) }}
      </span>
      <span class="brief__more">
        {{ expanded ? t('terminal.prompt.hide') : t('terminal.prompt.show') }}
      </span>
    </button>
    <div :id="bodyId">
      <p v-if="loading" class="brief__note">
        <Spinner size="sm" :label="t('terminal.prompt.loading')" />
      </p>
      <p v-if="error" class="brief__note brief__note--error" role="alert">
        {{ t('terminal.prompt.error', { error }) }}
      </p>
      <pre class="brief__text" :class="{ 'brief__text--preview': !expanded && truncated }">{{
        text
      }}</pre>
    </div>
  </div>
</template>

<style scoped>
.brief {
  border: var(--border-width) solid var(--term-border);
  border-radius: var(--radius-md);
}

.brief__head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  min-height: var(--control-md);
  padding: 0 var(--space-3);
  border: 0;
  background: transparent;
  color: var(--term-dim);
  font: inherit;
  font-size: var(--fs-xs);
  text-align: left;
  cursor: pointer;
}

.brief__head:focus-visible {
  outline: none;
  box-shadow: inset 0 0 0 var(--focus-ring-width) var(--border-focus);
  border-radius: var(--radius-md);
}

.brief__icon {
  font-size: var(--icon-sm);
}

.brief__title {
  color: var(--term-fg);
  font-weight: var(--fw-semibold);
}

.brief__size {
  flex: 1;
}

.brief__more {
  color: var(--term-accent);
}

.brief__text {
  max-height: 32em;
  margin: 0;
  padding: var(--space-2) var(--space-3) var(--space-3);
  overflow: auto;
  color: var(--term-fg);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.brief__text--preview {
  max-height: 6.5em;
  overflow: hidden;
  -webkit-mask-image: linear-gradient(to bottom, black 60%, transparent);
  mask-image: linear-gradient(to bottom, black 60%, transparent);
}

.brief__note {
  padding: 0 var(--space-3);
  font-size: var(--fs-xs);
}

.brief__note--error {
  color: var(--term-err);
}
</style>
