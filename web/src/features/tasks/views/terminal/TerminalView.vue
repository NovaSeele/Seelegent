<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowReactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { TaskSummary } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Icon from '@/components/ui/Icon.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useVirtualList } from '@/composables/useVirtualList'
import { sameArray, stableComputed } from '@/lib/stable'
import { useCommandsStore } from '@/stores/commands'
import { useTasksStore } from '@/stores/tasks'

import { buildTimeline, sameTimeline, type TimelineItem } from './timeline'
import TimelineEntry from './TimelineEntry.vue'

/**
 * The Terminal view: the worker transcript, newest at the bottom. The task's event log is retained while the
 * view is on screen; events fold into items (timeline.ts) and only the items near the viewport are rendered
 * (useVirtualList). The view follows new output while it is scrolled to the end; scrolling up stops that and
 * shows "Jump to latest". Older events load on demand from the top.
 */
const props = defineProps<{
  task: TaskSummary
  /** A timeline key to scroll to and highlight (a step opened from Tree or Graph). */
  reveal?: string | null
}>()

const { t } = useI18n()
const tasks = useTasksStore()
const taskId = props.task.id
const log = tasks.retainEvents(taskId)
onBeforeUnmount(() => tasks.releaseEvents(taskId))

let previous: TimelineItem[] = []
const items = computed(() => {
  const next = buildTimeline(log.items.value, previous)
  if (sameTimeline(previous, next)) return previous
  previous = next
  return next
})

const EARLIER = '@earlier'
const hasEarlier = computed(() => log.state.firstSeq > 1 && log.state.status === 'ready')
const keys = stableComputed(
  () => [...(hasEarlier.value ? [EARLIER] : []), ...items.value.map((item) => item.key)],
  sameArray,
)
const byKey = computed(() => new Map(items.value.map((item) => [item.key, item])))

const scroller = ref<HTMLElement | null>(null)
const { visible, padTop, padBottom, following, measure, scrollToEnd, scrollToKey } = useVirtualList(
  {
    scroller,
    keys,
    estimate: 32,
  },
)
/** The rendered window: the "load earlier" row or an item, by key. */
const rows = computed(() =>
  visible.value.map((key) => ({
    key,
    item: key === EARLIER ? null : (byKey.value.get(key) ?? null),
  })),
)

// Expanded items, kept here so an item scrolled out and back keeps its state. Failed tools start open.
const toggled = shallowReactive(new Set<string>())
const briefs = new Map<string, string>()
const isOpen = (item: TimelineItem) =>
  toggled.has(item.key) !== (item.kind === 'tool' && item.tool.state === 'failed')
function toggle(key: string) {
  if (toggled.has(key)) toggled.delete(key)
  else toggled.add(key)
}

// Reveal: wait until the item is in the list (the log may still be loading), then scroll and highlight it.
const flash = ref<string | null>(null)
let pendingReveal = props.reveal ?? null
let flashTimer: ReturnType<typeof setTimeout> | undefined
function tryReveal() {
  if (!pendingReveal || !scrollToKey(pendingReveal)) return
  flash.value = pendingReveal
  pendingReveal = null
  clearTimeout(flashTimer)
  flashTimer = setTimeout(() => (flash.value = null), 1600)
}
watch(
  () => props.reveal,
  (key) => {
    pendingReveal = key ?? null
    tryReveal()
  },
)
watch(keys, () => void nextTick(tryReveal), { flush: 'post' })
onMounted(() => void nextTick(tryReveal))
onBeforeUnmount(() => clearTimeout(flashTimer))

const unregister = useCommandsStore().register({
  id: 'terminal.end',
  title: { key: 'terminal.jumpCmd' },
  group: 'task',
  icon: 'arrowDown',
  shortcut: 'End',
  run: () => scrollToEnd(),
})
onBeforeUnmount(unregister)
</script>

<template>
  <div class="term">
    <div
      ref="scroller"
      class="term__scroll"
      role="log"
      tabindex="0"
      :aria-label="t('terminal.label')"
      :aria-busy="log.state.status === 'loading'"
    >
      <div :style="{ height: `${padTop}px` }" aria-hidden="true" />
      <template v-for="row in rows" :key="row.key">
        <div v-if="row.key === EARLIER" :ref="(el) => measure(el, row.key)" class="term__earlier">
          <Button
            size="sm"
            variant="ghost"
            :loading="log.state.loadingEarlier"
            @click="tasks.loadEarlier(taskId)"
          >
            {{ t('terminal.earlier') }}
          </Button>
        </div>
        <div
          v-else-if="row.item"
          :ref="(el) => measure(el, row.key)"
          :class="{ term__flash: row.key === flash }"
        >
          <TimelineEntry
            :item="row.item"
            :task-id="taskId"
            :cwd="task.cwd"
            :expanded="isOpen(row.item)"
            :briefs="briefs"
            @toggle="toggle(row.key)"
          />
        </div>
      </template>
      <div :style="{ height: `${padBottom}px` }" aria-hidden="true" />

      <p v-if="log.state.status === 'loading' && !items.length" class="term__state">
        <Spinner size="sm" :label="t('terminal.loading')" />
        <span>{{ t('terminal.loading') }}</span>
      </p>
      <div v-else-if="log.state.status === 'error'" class="term__state" role="alert">
        <span>{{ t('terminal.loadError', { error: log.state.error ?? '' }) }}</span>
        <Button size="sm" @click="tasks.retryEvents(taskId)">{{ t('common.action.retry') }}</Button>
      </div>
      <p v-else-if="log.state.status === 'ready' && !items.length" class="term__state">
        {{ t('terminal.empty') }}
      </p>
      <p
        v-if="log.state.error && log.state.status === 'ready'"
        class="term__state term__state--error"
        role="alert"
      >
        {{ t('terminal.earlierError', { error: log.state.error }) }}
      </p>
    </div>

    <button v-if="!following" type="button" class="term__jump" @click="scrollToEnd">
      <Icon name="arrowDown" />
      {{ t('terminal.jump') }}
    </button>
  </div>
</template>

<style scoped>
.term {
  position: relative;
  display: flex;
  /* Code reads as typed: "!=" stays two characters, never a ligature. */
  font-variant-ligatures: none;
  flex: 1;
  flex-direction: column;
  min-height: 0;
  background: var(--term-bg);
}

.term__scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-anchor: none;
  padding-bottom: var(--space-3);
}

.term__scroll:focus-visible {
  outline: none;
  box-shadow: inset 0 0 0 var(--focus-ring-width) var(--border-focus);
}

.term__flash {
  animation: term-flash 1.6s ease-out;
}

@keyframes term-flash {
  from {
    background: var(--accent-soft);
  }

  to {
    background: transparent;
  }
}

@media (prefers-reduced-motion: reduce) {
  .term__flash {
    animation: none;
    background: var(--accent-soft);
  }
}

.term__earlier {
  display: flex;
  justify-content: center;
  padding: var(--space-2);
}

.term__state {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-6) var(--space-4);
  color: var(--term-dim);
  font-size: var(--fs-sm);
  text-align: center;
}

.term__state--error {
  padding: var(--space-2) var(--space-4);
  color: var(--term-err);
}

.term__jump {
  position: absolute;
  right: var(--space-4);
  bottom: var(--space-3);
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  min-height: var(--control-sm);
  padding: 0 var(--space-3);
  border: var(--border-width) solid var(--accent-border);
  border-radius: var(--radius-pill);
  background: var(--bg-elevated);
  box-shadow: var(--shadow-md);
  color: var(--text-primary);
  font: inherit;
  font-size: var(--fs-xs);
  cursor: pointer;
}

.term__jump:focus-visible {
  outline: none;
  box-shadow: 0 0 0 var(--focus-ring-width) var(--border-focus);
}
</style>
