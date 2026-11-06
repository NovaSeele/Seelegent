/**
 * The one global keyboard dispatcher. Installed once by the app shell.
 *
 * Order for each keydown: Esc goes to the top overlay layer; while any layer is open every other global shortcut
 * is suspended (the overlay owns the keyboard); single keys never fire while typing in a field; two-step
 * sequences ("g t") wait up to SEQUENCE_MS for the second key.
 */
import { onBeforeUnmount, onMounted } from 'vue'

import {
  isModifierOnly,
  isTypingTarget,
  matchesChord,
  parseKeySpec,
  type Chord,
  type KeySpec,
} from '@/lib/keys'
import { useCommandsStore, type Command } from '@/stores/commands'
import { useLayersStore } from '@/stores/layers'

export const SEQUENCE_MS = 1000

interface Binding {
  command: Command
  spec: KeySpec
}

const hasModifier = (chord: Chord) => chord.ctrl || chord.meta || chord.alt

const sameChord = (a: Chord, b: Chord) =>
  a.key === b.key &&
  a.ctrl === b.ctrl &&
  a.alt === b.alt &&
  a.shift === b.shift &&
  a.meta === b.meta

export function createKeyDispatcher(
  commands: ReturnType<typeof useCommandsStore>,
  layers: ReturnType<typeof useLayersStore>,
  now: () => number = Date.now,
) {
  let pending: { first: Chord; at: number } | null = null

  function bindings(): Binding[] {
    const out: Binding[] = []
    for (const command of commands.available) {
      const spec = commands.shortcutFor(command)
      const parsed = spec ? parseKeySpec(spec) : null
      if (parsed) out.push({ command, spec: parsed })
    }
    return out
  }

  function fire(event: KeyboardEvent, command: Command) {
    event.preventDefault()
    pending = null
    void commands.run(command.id)
  }

  return function onKeydown(event: KeyboardEvent): void {
    if (event.defaultPrevented || event.isComposing) return
    if (event.key === 'Escape' && layers.anyOpen) {
      if (layers.escape()) event.preventDefault()
      return
    }
    if (layers.anyOpen || isModifierOnly(event)) return

    const typing = isTypingTarget(event.target)
    const list = bindings()

    if (pending && now() - pending.at <= SEQUENCE_MS) {
      const first = pending.first
      pending = null
      const second = list.find(
        (b) =>
          b.spec.length === 2 &&
          sameChord(b.spec[0] as Chord, first) &&
          matchesChord(b.spec[1] as Chord, event),
      )
      if (second) {
        fire(event, second.command)
        return
      }
    }
    pending = null

    for (const binding of list) {
      const [chord] = binding.spec
      if (binding.spec.length !== 1 || !chord || !matchesChord(chord, event)) continue
      if (typing && !(binding.command.allowInInput && hasModifier(chord))) continue
      fire(event, binding.command)
      return
    }

    if (typing) return
    const starter = list.find(
      (b) => b.spec.length === 2 && b.spec[0] && matchesChord(b.spec[0], event),
    )
    if (starter?.spec[0]) {
      event.preventDefault()
      pending = { first: starter.spec[0], at: now() }
    }
  }
}

/** Installs the dispatcher on window for the lifetime of the calling component. */
export function useShortcutDispatcher() {
  const handler = createKeyDispatcher(useCommandsStore(), useLayersStore())
  onMounted(() => window.addEventListener('keydown', handler))
  onBeforeUnmount(() => window.removeEventListener('keydown', handler))
}
