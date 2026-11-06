<script setup lang="ts" generic="T extends string">
import { computed, ref } from 'vue'

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
</script>

<template>
  <div ref="list" class="tabs" role="tablist" :aria-label="label">
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
