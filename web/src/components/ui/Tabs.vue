<script setup lang="ts" generic="T extends string">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

import { useRovingFocus } from '@/composables/useRovingFocus'

import Icon from './Icon.vue'
import type { IconName } from './icons'

export interface TabItem<V> {
  value: V
  label: string
  icon?: IconName
  count?: number
  disabled?: boolean
}

/**
 * Tab list (role=tablist) with roving focus and automatic activation. Panels are rendered by the parent;
 * give each panel `id="<idPrefix>-panel-<value>"`, `role="tabpanel"` and `aria-labelledby="<idPrefix>-tab-<value>"`.
 * When the tabs do not fit they scroll sideways: the edges fade while there is more that way (as the nav rail
 * does) and the selected tab is kept in view.
 */
const model = defineModel<T>({ required: true })

const props = defineProps<{
  items: readonly TabItem<T>[]
  label: string
  idPrefix: string
}>()

const list = ref<HTMLElement | null>(null)
const options = computed(() => props.items)
const { onKeydown } = useRovingFocus(options, (v) => (model.value = v), list)

const fadeStart = ref(false)
const fadeEnd = ref(false)
function updateFade() {
  const el = list.value
  if (!el) return
  fadeStart.value = el.scrollLeft > 1
  fadeEnd.value = el.scrollLeft + el.clientWidth < el.scrollWidth - 1
}

/** Scrolls the list (never the page) so the selected tab is fully visible, with a little room past it. */
function revealSelected() {
  const el = list.value
  const tab = el?.querySelector<HTMLElement>('[aria-selected="true"]')
  if (!el || !tab) return
  const margin = 24
  const left = tab.getBoundingClientRect().left - el.getBoundingClientRect().left + el.scrollLeft
  if (left - margin < el.scrollLeft) el.scrollLeft = Math.max(0, left - margin)
  else if (left + tab.offsetWidth + margin > el.scrollLeft + el.clientWidth) {
    el.scrollLeft = left + tab.offsetWidth + margin - el.clientWidth
  }
  updateFade()
}

watch(model, () => void nextTick(revealSelected))
let observer: ResizeObserver | undefined
onMounted(() => {
  revealSelected()
  updateFade()
  if (typeof ResizeObserver === 'undefined' || !list.value) return
  observer = new ResizeObserver(updateFade)
  observer.observe(list.value)
})
onBeforeUnmount(() => observer?.disconnect())
</script>

<template>
  <div
    ref="list"
    class="tabs"
    :class="{ 'tabs--fade-start': fadeStart, 'tabs--fade-end': fadeEnd }"
    role="tablist"
    :aria-label="label"
    @scroll.passive="updateFade"
  >
    <button
      v-for="(item, index) in items"
      :id="`${idPrefix}-tab-${item.value}`"
      :key="item.value"
      type="button"
      role="tab"
      class="tabs__tab"
      data-roving
      :aria-selected="item.value === model"
      :aria-controls="`${idPrefix}-panel-${item.value}`"
      :tabindex="item.value === model ? 0 : -1"
      :disabled="item.disabled"
      @click="model = item.value"
      @keydown="onKeydown($event, index)"
    >
      <Icon v-if="item.icon" :name="item.icon" class="tabs__icon" />
      <span>{{ item.label }}</span>
      <span v-if="item.count !== undefined" class="tabs__count">{{ item.count }}</span>
    </button>
  </div>
</template>

<style scoped>
.tabs {
  display: flex;
  gap: var(--space-1);
  overflow-x: auto;
  border-bottom: var(--border-width) solid var(--border-subtle);
  scrollbar-width: none;
  /* Edge fades, each on only while the tabs can scroll that way (mask alpha only; no theme colour). */
  --fade-start: 0px;
  --fade-end: 0px;
  mask-image: linear-gradient(
    to right,
    transparent 0,
    black var(--fade-start),
    black calc(100% - var(--fade-end)),
    transparent 100%
  );
}

.tabs--fade-start {
  --fade-start: var(--space-6);
}

.tabs--fade-end {
  --fade-end: var(--space-6);
}

.tabs__tab {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: var(--space-2);
  min-height: max(var(--control-lg), var(--target-min));
  padding: 0 var(--space-3);
  border: 0;
  background: transparent;
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  font-weight: var(--fw-medium);
  white-space: nowrap;
  cursor: pointer;
}

.tabs__tab::after {
  content: '';
  position: absolute;
  inset: auto var(--space-2) calc(-1 * var(--border-width));
  height: 2px;
  border-radius: var(--radius-pill);
  background: transparent;
}

.tabs__tab:hover {
  color: var(--text-primary);
}

.tabs__tab[aria-selected='true'] {
  color: var(--text-primary);
}

.tabs__tab[aria-selected='true']::after {
  background: var(--accent);
}

.tabs__tab:focus-visible {
  outline: none;
  box-shadow: inset 0 0 0 var(--focus-ring-width) var(--border-focus);
  border-radius: var(--radius-sm);
}

.tabs__icon {
  font-size: var(--icon-md);
}

.tabs__count {
  min-width: 18px;
  padding: 0 var(--space-1);
  border-radius: var(--radius-pill);
  background: var(--neutral-bg);
  color: var(--neutral-fg);
  font-size: var(--fs-2xs);
  font-variant-numeric: tabular-nums;
  line-height: 18px;
  text-align: center;
}
</style>
