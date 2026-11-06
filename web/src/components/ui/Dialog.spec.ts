import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { h, ref } from 'vue'

import { useCommandsStore } from '@/stores/commands'
import { useLayersStore } from '@/stores/layers'
import { flush, installKeys, press, setupApp } from '@/test/app'

import Dialog from './Dialog.vue'

describe('Dialog', () => {
  let uninstall: () => void

  beforeEach(() => {
    document.body.innerHTML = ''
  })

  afterEach(() => uninstall?.())

  async function mountDialog() {
    const { plugins } = await setupApp()
    uninstall = installKeys()
    const open = ref(false)
    const wrapper = mount(
      {
        setup: () => () => [
          h('button', { id: 'opener', onClick: () => (open.value = true) }, 'Open'),
          h(
            Dialog,
            { open: open.value, title: 'Title', 'onUpdate:open': (v: boolean) => (open.value = v) },
            {
              default: () => [h('button', { id: 'first' }, 'A'), h('button', { id: 'last' }, 'B')],
            },
          ),
        ],
      },
      { attachTo: document.body, global: { plugins } },
    )
    const opener = wrapper.get('#opener').element as HTMLButtonElement
    opener.focus()
    await wrapper.get('#opener').trigger('click')
    await flush()
    return { wrapper, open, opener }
  }

  it('moves focus in, traps Tab and Shift+Tab inside, and marks itself modal', async () => {
    const { wrapper } = await mountDialog()
    const dialog = document.querySelector('[role=dialog]') as HTMLElement
    expect(dialog.getAttribute('aria-modal')).toBe('true')
    expect(dialog.contains(document.activeElement)).toBe(true)

    const close = dialog.querySelector('button') as HTMLButtonElement
    expect(document.activeElement).toBe(close)

    ;(document.getElementById('last') as HTMLElement).focus()
    press('Tab')
    expect(document.activeElement).toBe(close)

    press('Tab', { shiftKey: true })
    expect(document.activeElement?.id).toBe('last')
    expect(document.documentElement.hasAttribute('data-scroll-locked')).toBe(true)
    wrapper.unmount()
  })

  it('closes on Esc and returns focus to the opener', async () => {
    const { wrapper, open, opener } = await mountDialog()
    expect(useLayersStore().stack).toHaveLength(1)
    press('Escape')
    await flush()
    expect(open.value).toBe(false)
    expect(document.querySelector('[role=dialog]')).toBeNull()
    expect(document.activeElement).toBe(opener)
    expect(useLayersStore().stack).toHaveLength(0)
    expect(document.documentElement.hasAttribute('data-scroll-locked')).toBe(false)
    wrapper.unmount()
  })

  it('suspends global shortcuts while open', async () => {
    const { wrapper } = await mountDialog()
    let ran = 0
    useCommandsStore().register({
      id: 'test.k',
      title: { key: 'palette.open' },
      group: 'general',
      shortcut: 'Mod+K',
      allowInInput: true,
      run: () => void ran++,
    })
    press('k', { ctrlKey: true, metaKey: false })
    press('k', { metaKey: true })
    expect(ran).toBe(0)
    wrapper.unmount()
  })
})
