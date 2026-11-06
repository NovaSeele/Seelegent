<script setup lang="ts">
import Icon from '@/components/ui/Icon.vue'

/** A group heading in the task list: a disclosure button that collapses or expands its tasks. */
defineProps<{
  rowKey: string
  label: string
  count: number
  collapsed: boolean
  focusable: boolean
}>()

defineEmits<{ toggle: []; focus: [] }>()
</script>

<template>
  <li class="group" role="none">
    <button
      type="button"
      class="group__btn"
      :data-key="rowKey"
      data-row
      :tabindex="focusable ? 0 : -1"
      :aria-expanded="!collapsed"
      @click="$emit('toggle')"
      @focus="$emit('focus')"
    >
      <Icon :name="collapsed ? 'chevronRight' : 'chevronDown'" class="group__chev" />
      <span class="group__name">{{ label }}</span>
      <span class="group__count">{{ count }}</span>
    </button>
  </li>
</template>

<style scoped>
.group {
  list-style: none;
}

.group__btn {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  min-height: var(--control-md);
  margin-top: var(--space-2);
  padding: 0 var(--space-3) 0 var(--space-2);
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-semibold);
  text-align: left;
  cursor: pointer;
}

.group__btn:hover {
  background: var(--bg-hover);
}

.group__btn:focus-visible {
  outline: none;
  box-shadow: inset 0 0 0 var(--focus-ring-width) var(--border-focus);
}

.group__chev {
  flex: none;
  font-size: var(--icon-sm);
}

.group__name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.group__count {
  color: var(--text-muted);
  font-variant-numeric: tabular-nums;
}
</style>
