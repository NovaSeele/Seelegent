import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { h } from 'vue'

import { flush, installKeys, press, setupApp } from '@/test/app'

import Menu, { type MenuItem } from './Menu.vue'

const ITEMS: MenuItem[] = [
  { id: 'copy', label: 'Copy' },
  { id: 'cut', label: 'Cut', disabled: true },
  { id: 'paste', label: 'Paste' },
  { id: 'delete', label: 'Delete', danger: true, separator: true },
]

describe('Menu', () => {
  let uninstall: () => void

  beforeEach(() => {
    document.body.innerHTML = ''
  })

  afterEach(() => uninstall?.())

  async function setup() {
    const { plugins } = await setupApp()
    uninstall = installKeys()
    const selected: string[] = []
    const wrapper = mount(
      {
        setup: () => () =>
          h(
            Menu,
            { items: ITEMS, label: 'Actions', onSelect: (id: string) => selected.push(id) },
            {
              trigger: ({ attrs }: { attrs: Record<string, unknown> }) =>
                h('button', { id: 'trigger', ...attrs }, 'More'),
            },
          ),
      },
      { attachTo: document.body, global: { plugins } },
    )
    const trigger = document.getElementById('trigger') as HTMLButtonElement
    trigger.focus()
    return { wrapper, trigger, selected }
  }

  const focused = () => document.activeElement?.textContent?.trim()

  it('opens from the keyboard on the first item and moves with arrows, Home and End', async () => {
    const { wrapper, trigger } = await setup()
    press('ArrowDown', {}, trigger)
    await flush()
    expect(trigger.getAttribute('aria-expanded')).toBe('true')
    expect(document.querySelector('[role=menu]')).not.toBeNull()
    expect(focused()).toBe('Copy')

    press('ArrowDown')
    await flush()
    expect(focused()).toBe('Paste') // skips the disabled item
    press('ArrowDown')
    await flush()
    expect(focused()).toBe('Delete')
    press('ArrowDown')
    await flush()
    expect(focused()).toBe('Copy') // wraps
    press('End')
    await flush()
    expect(focused()).toBe('Delete')
    press('Home')
    await flush()
    expect(focused()).toBe('Copy')
    wrapper.unmount()
  })

  it('opens on the last item with ArrowUp and jumps by typed letter', async () => {
    const { wrapper, trigger } = await setup()
    press('ArrowUp', {}, trigger)
    await flush()
    expect(focused()).toBe('Delete')
    press('p')
    await flush()
    expect(focused()).toBe('Paste')
    wrapper.unmount()
  })

  it('chooses with Enter and returns focus to the trigger', async () => {
    const { wrapper, trigger, selected } = await setup()
    press('Enter', {}, trigger)
    await flush()
    press('ArrowDown')
    await flush()
    press('Enter')
    await flush()
    expect(selected).toEqual(['paste'])
    expect(document.querySelector('[role=menu]')).toBeNull()
    expect(document.activeElement).toBe(trigger)
    wrapper.unmount()
  })

  it('closes on Esc and returns focus to the trigger', async () => {
    const { wrapper, trigger, selected } = await setup()
    press('ArrowDown', {}, trigger)
    await flush()
    press('Escape')
    await flush()
    expect(document.querySelector('[role=menu]')).toBeNull()
    expect(trigger.getAttribute('aria-expanded')).toBe('false')
    expect(document.activeElement).toBe(trigger)
    expect(selected).toEqual([])
    wrapper.unmount()
  })

  it('closes when its trigger leaves the document, and keeps a 0×0 trigger open but hidden', async () => {
    const { wrapper, trigger } = await setup()
    press('ArrowDown', {}, trigger)
    await flush()
    // jsdom gives every element a 0×0 box: the menu stays open, unseen until it can be placed.
    window.dispatchEvent(new Event('scroll'))
    await flush()
    const menu = document.querySelector<HTMLElement>('[role=menu]')
    expect(menu).not.toBeNull()
    expect(menu?.style.visibility).toBe('hidden')

    // The row re-rendered: the trigger is gone for good. The menu closes; with no trigger left, focus stays put.
    document.querySelector('.menu-anchor')?.remove()
    window.dispatchEvent(new Event('scroll'))
    await flush()
    expect(document.querySelector('[role=menu]')).toBeNull()
    expect(trigger.getAttribute('aria-expanded')).toBe('false')
    expect(trigger.isConnected).toBe(false)
    wrapper.unmount()
  })

  it('hands focus to its trigger when the trigger is back in the document after closing', async () => {
    const { wrapper, trigger } = await setup()
    press('ArrowDown', {}, trigger)
    await flush()
    expect(focused()).toBe('Copy')

    const anchor = document.querySelector('.menu-anchor') as HTMLElement
    const parent = anchor.parentNode as Node
    anchor.remove()
    window.dispatchEvent(new Event('scroll'))
    // Put back before the render settles (a kept-alive view coming back, a moved row).
    parent.appendChild(anchor)
    await flush()
    expect(document.querySelector('[role=menu]')).toBeNull()
    expect(document.activeElement).toBe(trigger)
    wrapper.unmount()
  })
})
