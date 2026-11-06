import { beforeEach, describe, expect, it } from 'vitest'

import { useCommandsStore, type Command } from '@/stores/commands'
import { useLayersStore } from '@/stores/layers'
import { setupApp } from '@/test/app'

import { createKeyDispatcher, SEQUENCE_MS } from './useShortcuts'

function keydown(key: string, init: KeyboardEventInit = {}, target?: HTMLElement) {
  const event = new KeyboardEvent('keydown', { key, cancelable: true, bubbles: true, ...init })
  if (target) Object.defineProperty(event, 'target', { value: target })
  return event
}

describe('key dispatcher', () => {
  let ran: string[]
  let clock: number
  let dispatch: (event: KeyboardEvent) => void

  beforeEach(async () => {
    await setupApp()
    ran = []
    clock = 0
    const commands = useCommandsStore()
    const cmd = (id: string, shortcut: string, extra: Partial<Command> = {}): Command => ({
      id,
      title: { key: 'palette.open' },
      group: 'general',
      shortcut,
      run: () => void ran.push(id),
      ...extra,
    })
    commands.register(
      cmd('palette', 'Ctrl+K', { allowInInput: true }),
      cmd('goTasks', 'g t'),
      cmd('help', '?'),
    )
    dispatch = createKeyDispatcher(commands, useLayersStore(), () => clock)
  })

  it('runs chords and two-step sequences', () => {
    const event = keydown('k', { ctrlKey: true })
    dispatch(event)
    expect(event.defaultPrevented).toBe(true)
    dispatch(keydown('g'))
    clock += 300
    dispatch(keydown('t'))
    expect(ran).toEqual(['palette', 'goTasks'])
  })

  it('forgets the first key of a sequence after SEQUENCE_MS', () => {
    dispatch(keydown('g'))
    clock += SEQUENCE_MS + 1
    dispatch(keydown('t'))
    expect(ran).toEqual([])
  })

  it('ignores single keys while typing but allows Mod chords marked allowInInput', () => {
    const input = document.createElement('input')
    dispatch(keydown('?', { shiftKey: true }, input))
    dispatch(keydown('g', {}, input))
    dispatch(keydown('t', {}, input))
    dispatch(keydown('k', { ctrlKey: true }, input))
    expect(ran).toEqual(['palette'])
  })

  it('suspends shortcuts while a layer is open and sends Esc to the top layer only', () => {
    const layers = useLayersStore()
    const escaped: string[] = []
    layers.push({ kind: 'dialog', modal: true, onEscape: () => escaped.push('dialog') })
    layers.push({ kind: 'menu', modal: false, onEscape: () => escaped.push('menu') })
    dispatch(keydown('k', { ctrlKey: true }))
    dispatch(keydown('?'))
    const esc = keydown('Escape')
    dispatch(esc)
    expect(ran).toEqual([])
    expect(escaped).toEqual(['menu'])
    expect(esc.defaultPrevented).toBe(true)
  })

  it('follows remapped and unbound shortcuts', () => {
    const commands = useCommandsStore()
    commands.setOverride('help', 'h')
    commands.setOverride('palette', null)
    dispatch(keydown('?'))
    dispatch(keydown('h'))
    dispatch(keydown('k', { ctrlKey: true }))
    expect(ran).toEqual(['help'])
  })
})
