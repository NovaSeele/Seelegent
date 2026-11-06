import { describe, expect, it } from 'vitest'

import {
  ariaKeyShortcuts,
  formatKeySpec,
  isTypingTarget,
  matchesChord,
  parseChord,
  parseKeySpec,
} from './keys'

const ev = (key: string, init: Partial<KeyboardEvent> = {}) =>
  ({
    key,
    code: '',
    ctrlKey: false,
    altKey: false,
    shiftKey: false,
    metaKey: false,
    ...init,
  }) as KeyboardEvent

describe('key specs', () => {
  it('maps Mod to Ctrl elsewhere and Cmd on macOS', () => {
    expect(parseChord('Mod+K', false)).toMatchObject({ ctrl: true, meta: false, key: 'k' })
    expect(parseChord('Mod+K', true)).toMatchObject({ ctrl: false, meta: true, key: 'k' })
  })

  it('parses two-step sequences and rejects longer or empty ones', () => {
    expect(parseKeySpec('g s', false)).toHaveLength(2)
    expect(parseKeySpec('g s x', false)).toBeNull()
    expect(parseKeySpec('  ', false)).toBeNull()
  })

  it('matches by physical key on non-Latin layouts', () => {
    const chord = parseChord('Mod+K', false)!
    expect(matchesChord(chord, ev('л', { code: 'KeyK', ctrlKey: true }))).toBe(true)
    expect(matchesChord(chord, ev('k', { ctrlKey: true, altKey: true }))).toBe(false)
  })

  it('matches Shift-produced symbols regardless of Shift', () => {
    expect(matchesChord(parseChord('?', false)!, ev('?', { shiftKey: true }))).toBe(true)
    expect(matchesChord(parseChord('Mod+,', false)!, ev(',', { ctrlKey: true }))).toBe(true)
  })

  it('formats for display and for aria-keyshortcuts', () => {
    expect(formatKeySpec('Mod+Shift+P', false)).toEqual([['Ctrl', 'Shift', 'P']])
    expect(formatKeySpec('Mod+K', true)).toEqual([['⌘', 'K']])
    expect(formatKeySpec('g s', false)).toEqual([['G'], ['S']])
    expect(ariaKeyShortcuts('Mod+K', false)).toBe('Control+K')
  })

  it('knows when the user is typing', () => {
    const input = document.createElement('input')
    const checkbox = Object.assign(document.createElement('input'), { type: 'checkbox' })
    expect(isTypingTarget(input)).toBe(true)
    expect(isTypingTarget(checkbox)).toBe(false)
    expect(isTypingTarget(document.createElement('textarea'))).toBe(true)
    expect(isTypingTarget(document.createElement('button'))).toBe(false)
  })
})
