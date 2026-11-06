/**
 * The shell with the real router: pages and NotFound render, settings opens as an overlay over the page you
 * came from (and over Watch on a cold open), and the app commands are searchable in the palette.
 */
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory } from 'vue-router'

import { createAppRouter } from '@/router'
import { useCommandsStore } from '@/stores/commands'
import { usePaletteStore } from '@/stores/palette'
import { setupApp } from '@/test/app'

import AppShell from './AppShell.vue'

describe('AppShell', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    localStorage.clear()
    // A wide desktop window: the rail layout with header and list side by side (jsdom has no matchMedia).
    vi.stubGlobal('matchMedia', (query: string) => ({
      matches: query.includes('width >='),
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
    }))
  })

  let unmount: () => void
  afterEach(() => {
    unmount?.()
    vi.unstubAllGlobals()
  })

  async function open(path: string) {
    const { plugins } = await setupApp()
    const router = createAppRouter(createMemoryHistory())
    await router.push(path)
    await router.isReady()
    const wrapper = mount(AppShell, {
      attachTo: document.body,
      global: { plugins: [...plugins, router] },
    })
    unmount = () => wrapper.unmount()
    await flushPromises()
    return { wrapper, router }
  }

  it('renders a page with its translated title and marks it in the navigation', async () => {
    const { wrapper } = await open('/proof')
    expect(wrapper.get('h1').text()).toBe('Proof')
    expect(document.title).toBe('Proof · Seelegent')
    expect(wrapper.get('[aria-current="page"]').attributes('href')).toBe('/proof')
  })

  it('shows each rail entry with a tooltip carrying its name and shortcut', async () => {
    const { wrapper } = await open('/')
    const members = wrapper.get('a.rail__item[href="/members"]')
    members.element.closest('.tooltip-anchor')?.dispatchEvent(new Event('pointerenter'))
    await new Promise((resolve) => setTimeout(resolve, 500))
    await flushPromises()
    const tip = document.querySelector('[role=tooltip]')
    expect(tip?.textContent?.replace(/\s+/g, '')).toBe('MembersG›E') // the sequence "g e"
  })

  it('shows NotFound for unknown addresses', async () => {
    const { wrapper } = await open('/no/such/page')
    expect(wrapper.text()).toContain('Page not found')
  })

  it('opens settings over the current page and keeps that page rendered', async () => {
    const { wrapper, router } = await open('/tasks')
    await router.push('/settings/language')
    await flushPromises()
    expect(wrapper.get('h1').text()).toBe('Tasks')
    const dialog = document.querySelector('[role=dialog]')
    expect(dialog?.textContent).toContain('Browser default')
    expect(document.getElementById('settings-section-title')?.textContent?.trim()).toBe('Language')
  })

  it('renders Watch behind settings on a cold open', async () => {
    const { wrapper } = await open('/settings/appearance')
    expect(wrapper.get('h1').text()).toBe('Watch')
    expect(document.querySelectorAll('[data-theme-preview]').length).toBeGreaterThan(26)
  })

  it('registers navigation, settings, theme and language commands', async () => {
    await open('/')
    const commands = useCommandsStore()
    const titles = (q: string) => commands.search(q).map((m) => m.title)
    expect(titles('')).toContain('Members')
    expect(titles('theme: dra')[0]).toBe('Theme: Dracula')
    expect(titles('language: tieng')[0]).toBe('Language: Tiếng Việt')
    expect(titles('')).not.toContain('Theme: Dracula') // per-theme entries only show when searched
    expect(commands.search('').filter((m) => m.command.id === 'settings.openSeq')).toHaveLength(0)
    await commands.run('theme.pick')
    expect(usePaletteStore().isOpen).toBe(true)
    expect(usePaletteStore().initialQuery).toBe('Theme: ')
  })
})
