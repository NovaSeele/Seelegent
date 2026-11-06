<script setup lang="ts">
import { computed, nextTick, ref, toRef, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import Icon from '@/components/ui/Icon.vue'
import Kbd from '@/components/ui/Kbd.vue'
import { PALETTE_ALT_SHORTCUT, PALETTE_SHORTCUT } from '@/config/nav'
import { useOverlay } from '@/composables/useOverlay'
import { matchesChord, parseKeySpec } from '@/lib/keys'
import { COMMAND_GROUPS, useCommandsStore, type CommandMatch } from '@/stores/commands'
import { usePaletteStore } from '@/stores/palette'

/**
 * Command palette (Mod+K, Mod+Shift+P): a combobox over the command registry. An empty query lists every command
 * by group; typing ranks them (diacritic-insensitive, English titles match too). Up/Down move, Enter runs,
 * Esc or Mod+K closes.
 */
const { t } = useI18n()
const palette = usePaletteStore()
const commands = useCommandsStore()
const id = useId()

const panel = ref<HTMLElement | null>(null)
const input = ref<HTMLInputElement | null>(null)
const list = ref<HTMLElement | null>(null)
const query = ref('')
const active = ref(0)

useOverlay(toRef(palette, 'isOpen'), panel, {
  kind: 'palette',
  onClose: () => palette.close(),
  initialFocus: () => input.value,
})

watch(
  () => palette.isOpen,
  (open) => {
    if (!open) return
    query.value = palette.initialQuery
    active.value = 0
  },
)

const matches = computed<CommandMatch[]>(() => (palette.isOpen ? commands.search(query.value) : []))
watch(query, () => (active.value = 0))

/** Rows with group headings when browsing (empty query); a flat ranked list when searching. */
const rows = computed(() => {
  const browsing = !query.value.trim()
  return matches.value.map((match, index) => ({
    match,
    index,
    heading:
      browsing && (index === 0 || matches.value[index - 1]?.command.group !== match.command.group)
        ? t(`palette.group.${match.command.group}`)
        : null,
  }))
})

const optionId = (index: number) => `${id}-opt-${index}`

function move(delta: number) {
  const count = matches.value.length
  if (count === 0) return
  active.value = (active.value + delta + count) % count
  void nextTick(() =>
    list.value?.querySelector(`#${CSS.escape(optionId(active.value))}`)?.scrollIntoView({
      block: 'nearest',
    }),
  )
}

async function runAt(index: number) {
  const match = matches.value[index]
  if (!match) return
  palette.close()
  await nextTick()
  await commands.execute(match.command)
}

const toggleChords = [PALETTE_SHORTCUT, PALETTE_ALT_SHORTCUT].flatMap(
  (spec) => parseKeySpec(spec) ?? [],
)

function onKeydown(event: KeyboardEvent) {
  if (event.isComposing) return
  if (toggleChords.some((chord) => matchesChord(chord, event))) {
    event.preventDefault()
    palette.close()
    return
  }
  const actions: Record<string, () => void> = {
    ArrowDown: () => move(1),
    ArrowUp: () => move(-1),
    PageDown: () => move(5),
    PageUp: () => move(-5),
    Enter: () => void runAt(active.value),
  }
  const action = actions[event.key]
  if (!action) return
  event.preventDefault()
  action()
}

function groupLabel(match: CommandMatch) {
  return COMMAND_GROUPS.includes(match.command.group)
    ? t(`palette.group.${match.command.group}`)
    : ''
}

function onBackdrop(event: MouseEvent) {
  if (event.target === event.currentTarget) palette.close()
}
</script>

<template>
  <Teleport to="body">
    <Transition name="palette">
      <div v-if="palette.isOpen" class="palette-backdrop" @mousedown="onBackdrop">
        <div
          ref="panel"
          class="palette"
          role="dialog"
          aria-modal="true"
          :aria-label="t('palette.title')"
        >
          <div class="palette__search">
            <Icon name="search" class="palette__search-icon" />
            <input
              ref="input"
              v-model="query"
              class="palette__input"
              type="text"
              role="combobox"
              aria-autocomplete="list"
              :aria-expanded="matches.length > 0"
              :aria-controls="`${id}-list`"
              :aria-activedescendant="matches.length ? optionId(active) : undefined"
              :aria-label="t('palette.title')"
              :placeholder="t('palette.placeholder')"
              autocomplete="off"
              spellcheck="false"
              @keydown="onKeydown"
            />
          </div>
          <ul
            :id="`${id}-list`"
            ref="list"
            class="palette__list"
            role="listbox"
            :aria-label="t('palette.results')"
          >
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
                @mousemove="active = row.index"
                @click="runAt(row.index)"
              >
                <Icon :name="row.match.command.icon ?? 'command'" class="palette__icon" />
                <span class="palette__title">{{ row.match.title }}</span>
                <span v-if="query.trim()" class="palette__group">{{ groupLabel(row.match) }}</span>
                <Kbd
                  v-if="commands.shortcutFor(row.match.command)"
                  :keys="commands.shortcutFor(row.match.command) ?? ''"
                  size="sm"
                />
              </li>
            </template>
          </ul>
          <p v-if="matches.length === 0" class="palette__empty" role="status">
            {{ t('palette.empty', { query: query.trim() }) }}
          </p>
          <footer class="palette__hints" aria-hidden="true">
            <span
              ><Kbd keys="Up" size="sm" /><Kbd keys="Down" size="sm" />
              {{ t('palette.hint.move') }}</span
            >
            <span><Kbd keys="Enter" size="sm" /> {{ t('palette.hint.run') }}</span>
            <span><Kbd keys="Escape" size="sm" /> {{ t('palette.hint.close') }}</span>
          </footer>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.palette-backdrop {
  position: fixed;
  inset: 0;
  z-index: var(--z-overlay);
  display: flex;
  justify-content: center;
  align-items: flex-start;
  padding: 12vh var(--space-4) var(--space-4);
  background: var(--bg-backdrop);
}

.palette {
  display: flex;
  flex-direction: column;
  width: min(640px, 100%);
  max-height: min(560px, 76vh);
  overflow: hidden;
  border: var(--border-width) solid var(--border-strong);
  border-radius: var(--radius-xl);
  background: var(--bg-elevated);
  box-shadow: var(--shadow-lg);
}

.palette__search {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: 0 var(--space-4);
  border-bottom: var(--border-width) solid var(--border-subtle);
}

.palette__search-icon {
  flex: none;
  color: var(--text-muted);
  font-size: var(--icon-lg);
}

.palette__input {
  flex: 1;
  min-width: 0;
  height: 52px;
  border: 0;
  background: none;
  color: var(--text-primary);
  font: inherit;
  font-size: var(--fs-lg);
  outline: none;
}

.palette__input::placeholder {
  color: var(--text-muted);
}

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

.palette__empty {
  padding: var(--space-6) var(--space-4);
  color: var(--text-secondary);
  text-align: center;
}

.palette__hints {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1) var(--space-4);
  padding: var(--space-2) var(--space-4);
  border-top: var(--border-width) solid var(--border-subtle);
  color: var(--text-muted);
  font-size: var(--fs-xs);
}

.palette__hints > span {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
}

.palette-enter-active,
.palette-leave-active {
  transition: opacity var(--duration-base) var(--ease-standard);
}

.palette-enter-active .palette,
.palette-leave-active .palette {
  transition: transform var(--duration-base) var(--ease-standard);
}

.palette-enter-from,
.palette-leave-to {
  opacity: 0;
}

.palette-enter-from .palette,
.palette-leave-to .palette {
  transform: translateY(-8px) scale(0.98);
}

@media (width < 600px) {
  .palette-backdrop {
    padding: var(--space-2);
  }

  .palette {
    max-height: calc(100dvh - 2 * var(--space-2));
  }

  .palette__hints {
    display: none;
  }
}
</style>
