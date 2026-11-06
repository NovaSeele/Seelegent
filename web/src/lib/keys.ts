/**
 * Keyboard shortcut specs: parsing, matching against KeyboardEvents and display.
 *
 * A spec is a chord ("Mod+K", "Mod+Shift+P", "Escape", "?") or a two-step sequence ("g s"). "Mod" is Cmd on
 * macOS and Ctrl elsewhere. Letters match by physical key as well, so Ctrl+K still works on a Russian layout.
 */
export interface Chord {
  readonly ctrl: boolean
  readonly alt: boolean
  readonly shift: boolean
  readonly meta: boolean
  /** Lowercase single character, or a KeyboardEvent.key name such as "Escape", "ArrowUp", "Enter". */
  readonly key: string
}

export type KeySpec = readonly Chord[]

/** Chords the browser owns; they can never be assigned (remapping refuses them). */
export const RESERVED_SPECS = ['Mod+W', 'Mod+T', 'Mod+N', 'Mod+L', 'Mod+Q'] as const

export function detectMac(): boolean {
  if (typeof navigator === 'undefined') return false
  return /mac|iphone|ipad/i.test(navigator.platform || navigator.userAgent)
}

const ALIASES: Readonly<Record<string, string>> = {
  esc: 'Escape',
  escape: 'Escape',
  enter: 'Enter',
  return: 'Enter',
  tab: 'Tab',
  space: ' ',
  up: 'ArrowUp',
  down: 'ArrowDown',
  left: 'ArrowLeft',
  right: 'ArrowRight',
  arrowup: 'ArrowUp',
  arrowdown: 'ArrowDown',
  arrowleft: 'ArrowLeft',
  arrowright: 'ArrowRight',
  home: 'Home',
  end: 'End',
  pageup: 'PageUp',
  pagedown: 'PageDown',
  backspace: 'Backspace',
  delete: 'Delete',
  plus: '+',
}

function normalizeKey(key: string): string {
  if (key.length === 1) return key.toLowerCase()
  return ALIASES[key.toLowerCase()] ?? key
}

export function parseChord(spec: string, mac = detectMac()): Chord | null {
  const parts = spec.trim().split('+')
  // "Mod++" or "+" names the plus key itself.
  if (spec.trim().endsWith('++') || spec.trim() === '+') {
    parts.splice(parts.length - 2, 2, '+')
  }
  let ctrl = false
  let alt = false
  let shift = false
  let meta = false
  let key = ''
  for (const part of parts) {
    const lower = part.toLowerCase()
    if (lower === 'mod') {
      if (mac) meta = true
      else ctrl = true
    } else if (lower === 'ctrl' || lower === 'control') ctrl = true
    else if (lower === 'alt' || lower === 'option') alt = true
    else if (lower === 'shift') shift = true
    else if (lower === 'meta' || lower === 'cmd') meta = true
    else if (part) key = normalizeKey(part)
  }
  return key ? { ctrl, alt, shift, meta, key } : null
}

/** Parses "Mod+K" or a sequence "g s"; returns null for an empty or malformed spec. */
export function parseKeySpec(spec: string, mac = detectMac()): KeySpec | null {
  const steps = spec.trim().split(/\s+/).filter(Boolean)
  if (steps.length === 0 || steps.length > 2) return null
  const chords = steps.map((step) => parseChord(step, mac))
  return chords.every((chord): chord is Chord => chord !== null) ? chords : null
}

type KeyEventLike = Pick<
  KeyboardEvent,
  'key' | 'code' | 'ctrlKey' | 'altKey' | 'shiftKey' | 'metaKey'
>

/** The event's key, falling back to the physical key for letters and digits on non-Latin layouts. */
export function eventKey(event: KeyEventLike): string {
  const key = normalizeKey(event.key)
  if (key.length === 1 && !/[\x20-\x7e]/.test(key)) {
    const letter = /^Key([A-Z])$/.exec(event.code)
    if (letter?.[1]) return letter[1].toLowerCase()
    const digit = /^Digit(\d)$/.exec(event.code)
    if (digit?.[1]) return digit[1]
  }
  return key
}

/** Symbols that need Shift on many layouts ("?" "!" ...) match regardless of Shift. */
const shiftAgnostic = (key: string) => key.length === 1 && !/[a-z0-9]/.test(key)

export function matchesChord(chord: Chord, event: KeyEventLike): boolean {
  if (chord.ctrl !== event.ctrlKey || chord.alt !== event.altKey || chord.meta !== event.metaKey) {
    return false
  }
  if (!shiftAgnostic(chord.key) && chord.shift !== event.shiftKey) return false
  return eventKey(event) === chord.key
}

/** True for keydown events of bare modifier keys, which never complete a chord. */
export function isModifierOnly(event: Pick<KeyboardEvent, 'key'>): boolean {
  return ['Control', 'Alt', 'Shift', 'Meta', 'AltGraph', 'CapsLock'].includes(event.key)
}

const DISPLAY: Readonly<Record<string, string>> = {
  Escape: 'Esc',
  ' ': 'Space',
  ArrowUp: '↑',
  ArrowDown: '↓',
  ArrowLeft: '←',
  ArrowRight: '→',
  Enter: 'Enter',
  Backspace: '⌫',
}

/** Display labels for each key of each step, e.g. [["Ctrl", "K"]] or [["G"], ["S"]]. */
export function formatKeySpec(spec: string, mac = detectMac()): string[][] {
  const parsed = parseKeySpec(spec, mac)
  if (!parsed) return []
  return parsed.map((chord) => {
    const keys: string[] = []
    if (chord.ctrl) keys.push(mac ? '⌃' : 'Ctrl')
    if (chord.alt) keys.push(mac ? '⌥' : 'Alt')
    if (chord.shift) keys.push(mac ? '⇧' : 'Shift')
    if (chord.meta) keys.push(mac ? '⌘' : 'Win')
    keys.push(DISPLAY[chord.key] ?? (chord.key.length === 1 ? chord.key.toUpperCase() : chord.key))
    return keys
  })
}

/** aria-keyshortcuts value ("Control+K", "G S"). */
export function ariaKeyShortcuts(spec: string, mac = detectMac()): string {
  const parsed = parseKeySpec(spec, mac)
  if (!parsed) return ''
  return parsed
    .map((chord) =>
      [
        chord.ctrl && 'Control',
        chord.alt && 'Alt',
        chord.shift && 'Shift',
        chord.meta && 'Meta',
        chord.key === ' ' ? 'Space' : chord.key.length === 1 ? chord.key.toUpperCase() : chord.key,
      ]
        .filter(Boolean)
        .join('+'),
    )
    .join(' ')
}

/** True when focus is in a place where typing must not trigger single-key shortcuts. */
export function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  if (target.isContentEditable) return true
  const tag = target.tagName
  if (tag === 'TEXTAREA' || tag === 'SELECT') return true
  if (tag !== 'INPUT') return false
  const type = (target as HTMLInputElement).type
  return !['checkbox', 'radio', 'button', 'submit', 'reset', 'range', 'color'].includes(type)
}
