<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import FileIcon from '@/components/ui/FileIcon.vue'
import Icon from '@/components/ui/Icon.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useFormat } from '@/composables/useFormat'
import { relativeTo } from '@/lib/paths'

import type { TimelineItem } from './timeline'

/** A tool call and its result: what ran, on which file, how it ended, and its output on demand. */
const props = defineProps<{
  item: Extract<TimelineItem, { kind: 'tool' }>
  cwd: string
  expanded: boolean
}>()
defineEmits<{ toggle: [] }>()

const { t } = useI18n()
const format = useFormat()

const tool = computed(() => props.item.tool)
const path = computed(() => {
  const file = props.item.file?.path
  if (file) return file
  const input = tool.value.input
  if (input && typeof input === 'object' && 'path' in input) {
    const value = (input as { path: unknown }).path
    if (typeof value === 'string') return value
  }
  return ''
})

const summary = computed(() => {
  if (tool.value.command) return tool.value.command
  if (path.value) return relativeTo(path.value, props.cwd)
  const input = tool.value.input
  if (input === undefined) return ''
  const text = typeof input === 'string' ? input : JSON.stringify(input)
  return text.length > 160 ? `${text.slice(0, 160)}…` : text
})

const output = computed(() => tool.value.output ?? '')
const outputId = computed(() => `tool-out-${props.item.key}`)
</script>

<template>
  <div class="tool" :class="`tool--${tool.state}`">
    <div class="tool__head">
      <Spinner v-if="tool.state === 'calling'" size="sm" decorative />
      <FileIcon v-else-if="path && !tool.command" :path="path" />
      <Icon v-else :name="tool.state === 'failed' ? 'error' : 'terminal'" class="tool__icon" />
      <span class="tool__name">{{ tool.name }}</span>
      <code class="tool__summary" :title="summary">{{ summary }}</code>
      <span v-if="tool.blocked" class="tool__flag tool__flag--bad">{{
        t('terminal.tool.blocked')
      }}</span>
      <span v-else-if="tool.state === 'failed'" class="tool__flag tool__flag--bad">
        {{ t('terminal.tool.failed') }}
      </span>
      <span v-else-if="tool.state === 'calling'" class="tool__flag">
        {{ t('terminal.tool.running') }}
      </span>
      <span v-if="tool.durationMs !== undefined" class="tool__time">
        {{ format.duration(tool.durationMs) }}
      </span>
      <button
        v-if="output"
        type="button"
        class="tool__toggle"
        :aria-expanded="expanded"
        :aria-controls="outputId"
        @click="$emit('toggle')"
      >
        <Icon :name="expanded ? 'chevronUp' : 'chevronDown'" />
        <span class="tool__toggle-label">
          {{ expanded ? t('terminal.tool.hide') : t('terminal.tool.show') }}
        </span>
      </button>
    </div>
    <pre v-if="output && expanded" :id="outputId" class="tool__out">{{ output }}</pre>
  </div>
</template>

<style scoped>
.tool {
  border-left: 2px solid var(--term-tool);
  padding-left: var(--space-2);
}

.tool--failed {
  border-left-color: var(--term-err);
}

.tool__head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
  min-height: var(--control-sm);
  font-size: var(--fs-xs);
}

.tool__icon {
  flex: none;
  font-size: var(--icon-sm);
  color: var(--term-tool);
}

.tool--failed .tool__icon {
  color: var(--term-err);
}

.tool__name {
  flex: none;
  color: var(--term-tool);
  font-weight: var(--fw-semibold);
}

.tool__summary {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  color: var(--term-path);
  font-family: var(--font-mono);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool__flag {
  flex: none;
  color: var(--term-dim);
}

.tool__flag--bad {
  color: var(--term-err);
  font-weight: var(--fw-semibold);
}

.tool__time {
  flex: none;
  color: var(--term-dim);
  font-variant-numeric: tabular-nums;
}

.tool__toggle {
  display: inline-flex;
  flex: none;
  align-items: center;
  gap: var(--space-1);
  justify-content: center;
  min-width: var(--target-min);
  min-height: max(var(--control-sm), var(--target-min));
  padding: 0 var(--space-2);
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--term-dim);
  font: inherit;
  cursor: pointer;
}

.tool__toggle:hover {
  background: var(--bg-hover);
  --text-muted: var(--text-primary);
  --text-secondary: var(--text-primary);
  color: var(--term-fg);
}

.tool__toggle:focus-visible {
  outline: none;
  box-shadow: 0 0 0 var(--focus-ring-width) var(--border-focus);
}

.tool__out {
  max-height: 24em;
  margin: var(--space-1) 0 var(--space-1);
  padding: var(--space-2) var(--space-3);
  overflow: auto;
  border-radius: var(--radius-sm);
  background: var(--bg-code);
  color: var(--term-fg);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

@media (width < 600px) {
  .tool__toggle-label {
    display: none;
  }
}
</style>
