<script setup lang="ts">
import { computed, nextTick, ref, useId } from 'vue'

import type { Placement } from '@/composables/useFloating'

import Icon from './Icon.vue'
import type { IconName } from './icons'
import Kbd from './Kbd.vue'
import Popover from './Popover.vue'

export interface MenuItem {
  id: string
  label: string
  icon?: IconName
  shortcut?: string
  danger?: boolean
  disabled?: boolean
  /** Draws a separator above this item. */
  separator?: boolean
}

/**
 * Action menu (WAI-ARIA menu button). The trigger slot receives `attrs` to bind on its button.
 * Keys: Enter / Space / ArrowDown open on the first item, ArrowUp on the last; ArrowUp/Down, Home/End move;
 * typing a letter jumps to the next item starting with it; Enter / Space choose; Esc or Tab close, Esc returns
 * focus to the trigger.
 */
const props = withDefaults(
  defineProps<{
    items: readonly MenuItem[]
    label: string
    placement?: Placement
  }>(),
  { placement: 'bottom-end' },
)

const emit = defineEmits<{ select: [id: string] }>()

const id = useId()
const open = ref(false)
const anchor = ref<HTMLElement | null>(null)
const itemEls = ref<HTMLElement[]>([])
const active = ref(-1)

const enabled = computed(() =>
  props.items.map((item, index) => (item.disabled ? -1 : index)).filter((i) => i >= 0),
)

function triggerEl(): HTMLElement | null {
  return anchor.value?.querySelector<HTMLElement>('button, a, [tabindex]') ?? null
}

async function focusItem(index: number) {
  active.value = index
  await nextTick()
  itemEls.value[index]?.focus()
}

async function openMenu(at: 'first' | 'last' = 'first') {
  open.value = true
  await nextTick()
  const list = enabled.value
  const target = at === 'first' ? list[0] : list[list.length - 1]
  if (target !== undefined) await focusItem(target)
}

function closeMenu(returnFocus: boolean) {
  open.value = false
  active.value = -1
  if (returnFocus) triggerEl()?.focus()
}

function toggle() {
  if (open.value) closeMenu(false)
  else void openMenu('first')
}

function onTriggerKeydown(event: KeyboardEvent) {
  if (event.key === 'ArrowDown' || event.key === 'Enter' || event.key === ' ') {
    event.preventDefault()
    void openMenu('first')
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    void openMenu('last')
  }
}

function move(step: 1 | -1) {
  const list = enabled.value
  if (list.length === 0) return
  const pos = list.indexOf(active.value)
  const next = list[(pos + step + list.length) % list.length]
  if (next !== undefined) void focusItem(next)
}

function choose(item: MenuItem) {
  if (item.disabled) return
  closeMenu(true)
  emit('select', item.id)
}

function onMenuKeydown(event: KeyboardEvent) {
  const list = enabled.value
  switch (event.key) {
    case 'ArrowDown':
      event.preventDefault()
      move(1)
      break
    case 'ArrowUp':
      event.preventDefault()
      move(-1)
      break
    case 'Home':
      event.preventDefault()
      if (list[0] !== undefined) void focusItem(list[0])
      break
    case 'End':
      event.preventDefault()
      if (list.length) void focusItem(list[list.length - 1] as number)
      break
    case 'Tab':
      closeMenu(false)
      break
    case 'Enter':
    case ' ': {
      event.preventDefault()
      const item = props.items[active.value]
      if (item) choose(item)
      break
    }
    default:
      if (event.key.length === 1 && /\S/.test(event.key)) typeahead(event.key)
  }
}

function typeahead(char: string) {
  const lower = char.toLocaleLowerCase()
  const list = enabled.value
  const start = list.indexOf(active.value)
  for (let step = 1; step <= list.length; step++) {
    const index = list[(start + step) % list.length]
    if (index === undefined) continue
    if (props.items[index]?.label.toLocaleLowerCase().startsWith(lower)) {
      void focusItem(index)
      return
    }
  }
}

function onPopoverClose(reason: 'escape' | 'outside') {
  closeMenu(reason === 'escape')
}

const triggerAttrs = computed(() => ({
  'aria-haspopup': 'menu' as const,
  'aria-expanded': open.value,
  'aria-controls': open.value ? id : undefined,
  onClick: toggle,
  onKeydown: onTriggerKeydown,
}))
</script>

<template>
  <span ref="anchor" class="menu-anchor">
    <slot name="trigger" :attrs="triggerAttrs" :open="open" />
  </span>
  <Popover
    :id="id"
    :open="open"
    :anchor="anchor"
    :placement="placement"
    role="menu"
    :label="label"
    kind="menu"
    @close="onPopoverClose"
    @keydown="onMenuKeydown"
  >
    <template v-for="(item, index) in items" :key="item.id">
      <div v-if="item.separator" class="menu__separator" role="separator" />
      <button
        :ref="(el) => (itemEls[index] = el as HTMLElement)"
        type="button"
        role="menuitem"
        class="menu__item"
        :class="{ 'menu__item--danger': item.danger }"
        :tabindex="index === active ? 0 : -1"
        :aria-disabled="item.disabled || undefined"
        @click="choose(item)"
        @pointermove="!item.disabled && (active = index)"
        @focus="active = index"
      >
        <Icon v-if="item.icon" :name="item.icon" class="menu__icon" />
        <span class="menu__label">{{ item.label }}</span>
        <Kbd v-if="item.shortcut" :keys="item.shortcut" size="sm" />
      </button>
    </template>
  </Popover>
</template>

<style scoped>
.menu-anchor {
  display: inline-flex;
}

.menu__item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  min-height: max(var(--control-md), var(--target-min));
  padding: 0 var(--space-2);
  border: 0;
  border-radius: max(0px, calc(var(--radius-lg) - var(--space-1)));
  background: transparent;
  color: var(--text-primary);
  font-size: var(--fs-sm);
  text-align: start;
  cursor: pointer;
}

.menu__item:focus {
  outline: none;
  background: var(--bg-hover);
  --text-muted: var(--text-primary);
  --text-secondary: var(--text-primary);
}

.menu__item:focus-visible {
  box-shadow: inset 0 0 0 var(--focus-ring-width) var(--border-focus);
}

.menu__item[aria-disabled='true'] {
  color: var(--text-muted);
  cursor: not-allowed;
}

.menu__item--danger {
  color: var(--status-danger-fg);
}

.menu__icon {
  flex: none;
  color: var(--text-muted);
  font-size: var(--icon-md);
}

.menu__item--danger .menu__icon {
  color: inherit;
}

.menu__label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.menu__separator {
  height: var(--border-width);
  margin: var(--space-1) calc(-1 * var(--space-1));
  background: var(--border-subtle);
}
</style>
