/**
 * Command registry: everything the palette lists and the keyboard can trigger. A command is registered by the
 * feature that owns it and unregistered when that feature unmounts (register() returns the undo function).
 * Titles and search keywords are i18n keys, resolved in the active language and in English, so English words
 * always work too. Shortcuts are default specs with a per-command override map, ready for remapping.
 */
import { defineStore } from 'pinia'
import { computed, shallowRef } from 'vue'

import type { IconName } from '@/components/ui/icons'
import { i18n } from '@/i18n'
import { bestScore } from '@/lib/fuzzy'

export type CommandGroup =
  'navigation' | 'task' | 'tasks' | 'settings' | 'appearance' | 'language' | 'general'

export const COMMAND_GROUPS: readonly CommandGroup[] = [
  'navigation',
  'task',
  'tasks',
  'general',
  'settings',
  'appearance',
  'language',
]

export interface Text {
  key: string
  /** Named values; a Text value is itself translated (in the same language). */
  params?: Record<string, string | number | Text>
}

export interface Command {
  id: string
  title: Text
  group: CommandGroup
  /** Extra search words (an i18n key whose value is a space-separated list). */
  keywords?: Text
  icon?: IconName
  /** Default key spec ("Mod+K", "g t"). */
  shortcut?: string
  /** Listed only when the query matches (e.g. one entry per theme). */
  hidden?: boolean
  /** false: keyboard only, never listed (a second shortcut for another command). */
  listed?: boolean
  /** Also fires while typing in a text field (only sensible for Mod chords). */
  allowInInput?: boolean
  when?: () => boolean
  run: () => void | Promise<void>
}

export interface CommandMatch {
  command: Command
  title: string
  score: number
}

export const MAX_RESULTS = 50

/**
 * Commands made on the fly from live data (one per task), listed only while searching. A source gets the query
 * and returns its candidates; they are ranked with the registered commands and run directly, never registered.
 */
export type CommandSource = (query: string) => readonly Command[]

export function resolveText(text: Text, locale?: string): string {
  const t = i18n.global.t as (
    key: string,
    named: Record<string, unknown>,
    options?: object,
  ) => string
  const named: Record<string, string | number> = {}
  for (const [name, value] of Object.entries(text.params ?? {})) {
    named[name] = typeof value === 'object' ? resolveText(value, locale) : value
  }
  return locale ? t(text.key, named, { locale }) : t(text.key, named)
}

export const useCommandsStore = defineStore('commands', () => {
  const commands = shallowRef<ReadonlyMap<string, Command>>(new Map())
  const sources = shallowRef<readonly CommandSource[]>([])
  /** User remaps: command id -> spec, or null to unbind. Filled from settings later. */
  const overrides = shallowRef<Readonly<Record<string, string | null>>>({})

  function register(...list: Command[]): () => void {
    const next = new Map(commands.value)
    for (const command of list) next.set(command.id, command)
    commands.value = next
    return () => {
      const remaining = new Map(commands.value)
      for (const command of list) {
        if (remaining.get(command.id) === command) remaining.delete(command.id)
      }
      commands.value = remaining
    }
  }

  function addSource(source: CommandSource): () => void {
    sources.value = [...sources.value, source]
    return () => (sources.value = sources.value.filter((s) => s !== source))
  }

  const available = computed(() =>
    [...commands.value.values()].filter((command) => !command.when || command.when()),
  )

  function shortcutFor(command: Command): string | undefined {
    if (command.id in overrides.value) return overrides.value[command.id] ?? undefined
    return command.shortcut
  }

  function setOverride(id: string, spec: string | null | undefined) {
    const next = { ...overrides.value }
    if (spec === undefined) delete next[id]
    else next[id] = spec
    overrides.value = next
  }

  async function run(id: string): Promise<boolean> {
    const command = commands.value.get(id)
    return command ? execute(command) : false
  }

  /** Runs a command object (a registered one or one a source made for this search). */
  async function execute(command: Command): Promise<boolean> {
    if (command.when && !command.when()) return false
    await command.run()
    return true
  }

  /** Ranked matches for the palette; an empty query lists every visible command in group order. */
  function search(query: string): CommandMatch[] {
    const list = available.value.filter((command) => command.listed !== false)
    const trimmed = query.trim()
    if (!trimmed) {
      return list
        .filter((command) => !command.hidden)
        .map((command) => ({ command, title: resolveText(command.title), score: 0 }))
        .sort(
          (a, b) =>
            COMMAND_GROUPS.indexOf(a.command.group) - COMMAND_GROUPS.indexOf(b.command.group),
        )
    }
    const scored: CommandMatch[] = []
    const extra = sources.value.flatMap((source) => source(trimmed))
    ;[...list, ...extra].forEach((command) => {
      const title = resolveText(command.title)
      const texts: [string, number][] = [
        [title, 1],
        [resolveText(command.title, 'en'), 0.9],
      ]
      if (command.keywords) {
        texts.push([resolveText(command.keywords), 0.8], [resolveText(command.keywords, 'en'), 0.7])
      }
      const score = bestScore(trimmed, texts)
      if (score > 0) scored.push({ command, title, score })
    })
    // Stable sort: equal scores keep registration order (pages stay in rail order).
    return scored.sort((a, b) => b.score - a.score).slice(0, MAX_RESULTS)
  }

  return {
    commands,
    available,
    overrides,
    register,
    addSource,
    shortcutFor,
    setOverride,
    run,
    execute,
    search,
  }
})
