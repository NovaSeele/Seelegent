<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import Icon from '@/components/ui/Icon.vue'
import Kbd from '@/components/ui/Kbd.vue'
import { COMMAND_GROUPS, useCommandsStore, type CommandMatch } from '@/stores/commands'

export interface PaletteRow {
  match: CommandMatch
  index: number
  /** Group heading drawn above this row (browsing only). */
  heading: string | null
}

/**
 * The palette's listbox. The input keeps focus (aria-activedescendant names the active option), so this list
 * only scrolls the active option into view and reports hover and clicks.
 */
const props = defineProps<{
  id: string
  rows: readonly PaletteRow[]
  active: number
  /** Searching shows each command's group beside it (headings are only drawn while browsing). */
  searching: boolean
  optionId: (index: number) => string
}>()
const emit = defineEmits<{ hover: [index: number]; run: [index: number] }>()

const { t } = useI18n()
const commands = useCommandsStore()
const list = ref<HTMLElement | null>(null)

watch(
  () => props.active,
  (index) =>
    void nextTick(() =>
      list.value
        ?.querySelector(`#${CSS.escape(props.optionId(index))}`)
        ?.scrollIntoView({ block: 'nearest' }),
    ),
)

function groupLabel(match: CommandMatch) {
  return COMMAND_GROUPS.includes(match.command.group)
    ? t(`palette.group.${match.command.group}`)
    : ''
}
</script>

<template>
  <ul :id="id" ref="list" class="palette__list" role="listbox" :aria-label="t('palette.results')">
    <template v-for="row in rows" :key="row.match.command.id">
      <li v-if="row.heading" class="palette__heading" role="presentation">
        {{ row.heading }}
      </li>
      <li
        :id="optionId(row.index)"
        class="palette__option"
        :class="{ 'palette__option--active': row.index === active }"
        role="option"
        :aria-selected="row.index === active"
        @mousemove="emit('hover', row.index)"
        @click="emit('run', row.index)"
      >
        <Icon :name="row.match.command.icon ?? 'command'" class="palette__icon" />
        <span class="palette__title">{{ row.match.title }}</span>
        <span v-if="searching" class="palette__group">{{ groupLabel(row.match) }}</span>
        <Kbd
          v-if="commands.shortcutFor(row.match.command)"
          :keys="commands.shortcutFor(row.match.command) ?? ''"
          size="sm"
        />
      </li>
    </template>
  </ul>
</template>

<style scoped>
.palette__list {
  flex: 1;
  min-height: 0;
  margin: 0;
  padding: var(--space-2);
  overflow-y: auto;
  overscroll-behavior: contain;
  list-style: none;
}

.palette__list:empty {
  display: none;
}

.palette__heading {
  padding: var(--space-3) var(--space-3) var(--space-1);
  color: var(--text-muted);
  font-size: var(--fs-2xs);
  font-weight: var(--fw-semibold);
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.palette__heading:first-child {
  padding-top: var(--space-1);
}

/* Concentric: options sit 8px inside a 16px panel, so they take radius-md (16 - 8). */
.palette__option {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  min-height: max(var(--control-lg), var(--target-min));
  padding: var(--space-1) var(--space-3);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  cursor: pointer;
}

.palette__option--active {
  background: var(--accent-soft);
}

.palette__icon {
  flex: none;
  color: var(--text-secondary);
  font-size: var(--icon-md);
}

.palette__option--active .palette__icon {
  color: var(--accent);
}

.palette__title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.palette__group {
  flex: none;
  color: var(--text-muted);
  font-size: var(--fs-xs);
}
</style>
