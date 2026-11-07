<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import FileIcon from '@/components/ui/FileIcon.vue'
import Icon from '@/components/ui/Icon.vue'
import RichText from '@/components/RichText.vue'
import { useFormat } from '@/composables/useFormat'
import { relativeTo } from '@/lib/paths'

import PromptItem from './PromptItem.vue'
import { turnTokens, type TimelineItem } from './timeline'
import ToolItem from './ToolItem.vue'

/** One transcript item. Simple kinds render here; tools and the worker brief have their own components. */
const props = defineProps<{
  item: TimelineItem
  taskId: string
  cwd: string
  expanded: boolean
  briefs: Map<string, string>
}>()
defineEmits<{ toggle: [] }>()

const { t } = useI18n()
const format = useFormat()

const noticeText = computed(() => {
  const item = props.item
  if (item.kind !== 'notice') return ''
  const text = t(
    item.message.key,
    item.message.params ?? {},
    (item.message.params?.count as number) ?? 1,
  )
  return item.until
    ? `${text} · ${t('terminal.until', { time: format.value.time(item.until) })}`
    : text
})
</script>

<template>
  <div class="te" :class="`te--${item.kind}`" :data-seq="item.key">
    <template v-if="item.kind === 'lead'">
      <div class="te__lead">
        <div class="te__lead-meta">
          <span class="te__who">{{ t(`terminal.by.${item.by}`) }}</span>
          <span v-if="item.turn">{{ t('terminal.turn', { n: item.turn }) }}</span>
          <span v-if="item.model">{{ item.model }}</span>
          <span v-if="item.tier">{{ t(`task.followup.tier.${item.tier}`) }}</span>
          <time :datetime="item.ts">{{ format.time(item.ts) }}</time>
        </div>
        <RichText :text="item.message" class="te__lead-text" />
      </div>
    </template>

    <p v-else-if="item.kind === 'route'" class="te__line te__line--dim">
      <Icon name="accounts" class="te__icon" />
      <span>
        {{
          t(item.continuing ? 'terminal.route.continuing' : 'terminal.route.first', {
            account: item.accountLabel,
            model: item.model,
          })
        }}
        <template v-if="item.remaining !== undefined">
          · {{ t('terminal.route.left', { percent: format.percent(item.remaining) }) }}
        </template>
        <span class="te__muted"> · {{ item.reason }}</span>
      </span>
    </p>

    <PromptItem
      v-else-if="item.kind === 'prompt'"
      :item="item"
      :task-id="taskId"
      :expanded="expanded"
      :briefs="briefs"
      @toggle="$emit('toggle')"
    />

    <RichText v-else-if="item.kind === 'text'" :text="item.text" class="te__text" />

    <ToolItem
      v-else-if="item.kind === 'tool'"
      :item="item"
      :cwd="cwd"
      :expanded="expanded"
      @toggle="$emit('toggle')"
    />

    <p v-else-if="item.kind === 'file'" class="te__line te__line--dim">
      <FileIcon :path="item.path" lead />
      <span>{{ t(`terminal.file.${item.op}`) }}</span>
      <code class="te__path">{{ relativeTo(item.path, cwd) }}</code>
    </p>

    <div
      v-else-if="item.kind === 'report'"
      class="te__report"
      :class="`te__report--${item.report.status}`"
    >
      <p class="te__report-title">
        <Icon :name="item.report.status === 'success' ? 'success' : 'error'" class="te__icon" />
        {{ t(`terminal.report.${item.report.status}`) }}
        <span v-if="item.report.synthesized" class="te__muted">
          · {{ t('terminal.report.synthesized') }}
        </span>
      </p>
      <RichText v-if="item.report.response" :text="item.report.response" />
      <pre v-if="item.report.error" class="te__pre">{{ item.report.error }}</pre>
    </div>

    <p
      v-else-if="item.kind === 'turnEnd'"
      class="te__turn-end"
      :class="`te__turn-end--${item.status}`"
    >
      <span>{{ t(`terminal.turnEnd.${item.status}`, { n: item.turn ?? '' }) }}</span>
      <span v-if="turnTokens(item)" class="te__muted">
        · {{ t('terminal.turnEnd.tokens', { n: format.compact(turnTokens(item)) }) }}
      </span>
      <span v-if="item.files.length" class="te__muted">
        · {{ t('terminal.turnEnd.files', item.files.length) }}
      </span>
    </p>

    <div v-else-if="item.kind === 'notice'" class="te__notice" :class="`te__notice--${item.tone}`">
      <p class="te__line">
        <Icon :name="item.icon" class="te__icon" />
        <span>{{ noticeText }}</span>
      </p>
      <pre v-if="item.detail" class="te__pre te__detail">{{ item.detail }}</pre>
    </div>
  </div>
</template>

<style scoped>
.te {
  padding: var(--space-1) var(--space-4);
  color: var(--term-fg);
  font-size: var(--fs-sm);
  line-height: var(--lh-prose);
}

.te--lead {
  padding-top: var(--space-4);
}

.te__lead {
  padding: var(--space-3) var(--space-4);
  border: var(--border-width) solid var(--term-border);
  border-radius: var(--radius-md);
  background: var(--term-prompt-bg);
  color: var(--term-prompt-fg);
}

.te__lead-meta {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-3);
  margin-bottom: var(--space-1);
  color: var(--term-dim);
  font-size: var(--fs-xs);
}

.te__who {
  color: var(--term-accent);
  font-weight: var(--fw-semibold);
}

.te__line {
  display: flex;
  align-items: baseline;
  gap: var(--space-2);
  min-width: 0;
}

.te__line--dim {
  color: var(--term-dim);
  font-size: var(--fs-xs);
}

.te__icon {
  flex: none;
  align-self: center;
  font-size: var(--icon-sm);
}

.te__muted {
  color: var(--term-dim);
}

.te__path {
  min-width: 0;
  overflow: hidden;
  color: var(--term-path);
  font-family: var(--font-mono);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.te__text {
  color: var(--term-fg);
}

.te__report {
  padding: var(--space-3) var(--space-4);
  border-left: 3px solid var(--term-ok);
  border-radius: var(--radius-sm);
  background: var(--bg-surface-2);
}

.te__report--error {
  border-left-color: var(--term-err);
}

.te__report-title {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin-bottom: var(--space-1);
  font-weight: var(--fw-semibold);
}

.te__turn-end {
  padding-top: var(--space-1);
  border-top: var(--border-width) dashed var(--term-border);
  color: var(--term-ok);
  font-size: var(--fs-xs);
}

.te__turn-end--failed {
  color: var(--term-err);
}

.te__turn-end--cancelled,
.te__turn-end--running {
  color: var(--term-dim);
}

.te__notice--dim {
  color: var(--term-dim);
  font-size: var(--fs-xs);
}

.te__notice--warn {
  color: var(--term-warn);
}

.te__notice--error {
  color: var(--term-err);
}

.te__notice--ok {
  color: var(--term-ok);
}

.te__notice--info {
  color: var(--term-accent);
}

.te__pre {
  margin-top: var(--space-1);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.te__detail {
  max-height: 16em;
  overflow: auto;
  padding-left: calc(var(--icon-sm) + var(--space-2));
  color: var(--term-fg);
}

@media (width < 600px) {
  .te {
    padding-inline: var(--space-3);
  }
}
</style>
