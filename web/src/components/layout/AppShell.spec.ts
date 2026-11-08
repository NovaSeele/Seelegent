/**
 * The shell with the real router: pages and NotFound render, settings opens as an overlay over the page you
 * came from (and over Watch on a cold open), and the app commands are searchable in the palette.
 */
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory } from 'vue-router'

import { createAppRouter } from '@/router'
import { useCommandsStore } from '@/stores/commands'
import { usePaletteStore } from '@/stores/palette'
import { useUiStore } from '@/stores/ui'
import { setupApp } from '@/test/app'

import AppShell from './AppShell.vue'

// The slow part of these tests was never the shell (F57): it was the first load of each lazy page, which compiles
// the page's whole import graph in this worker (the tasks page: about 250 ms cold, 10 ms warm; many times more on a
// loaded machine, and the width test visits seven pages). The pages are loaded once before the tests, under the
// hook's own limit, so each test measures only the shell and keeps the default timeout. The tests still wait for
// what they check instead of sleeping.
describe('AppShell', () => {
  beforeAll(async () => {
    const loaders = createAppRouter(createMemoryHistory())
      .getRoutes()
      .flatMap((route) => Object.values(route.components ?? {}))
      .filter((c): c is () => Promise<unknown> => typeof c === 'function')
    await Promise.all(loaders.map((load) => load()))
  }, 60_000)

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
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  /** Waits until check passes (content a lazy page renders), or fails with its last error. */
  const settle = (check: () => void) => vi.waitFor(check, { timeout: 10_000, interval: 20 })

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
    // jsdom lays nothing out; a tooltip whose anchor has no box stays closed (it would sit at 0,0), so give it one.
    const anchor = members.element.closest<HTMLElement>('.tooltip-anchor')
    if (anchor)
      vi.spyOn(anchor, 'getBoundingClientRect').mockReturnValue(new DOMRect(0, 120, 48, 40))
    // The tooltip's hover delay runs on fake timers: no real sleep that a busy machine can outrun.
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    anchor?.dispatchEvent(new Event('pointerenter'))
    vi.advanceTimersByTime(1000)
    vi.useRealTimers()
    await flushPromises()
    const tip = document.querySelector('[role=tooltip]')
    expect(tip?.textContent?.replace(/\s+/g, '')).toBe('MembersG›E') // the sequence "g e"
  })

  it('calls a page by the same name in the rail, the palette, its heading and the tab title', async () => {
    const { wrapper } = await open('/automations')
    const rail = wrapper.get('a.rail__item[href="/automations"] .rail__label').text()
    const palette = useCommandsStore().search('autom')[0]?.title
    expect([rail, palette, wrapper.get('h1').text()]).toEqual([
      'Automations',
      'Automations',
      'Automations',
    ])
    expect(document.title).toBe('Automations · Seelegent')
  })

  // Seven navigations, each loading a lazy page: each check waits for the page it expects.
  it('centres document pages and gives split views the full width, also under settings', async () => {
    const { wrapper, router } = await open('/usage')
    expect(wrapper.get('.page').attributes('data-page-width')).toBe('readable')
    for (const path of ['/issues', '/meetings', '/contests', '/members']) {
      await router.push(path)
      await settle(() =>
        expect(wrapper.get('.page').attributes('data-page-width'), path).toBe('readable'),
      )
    }
    await router.push('/tasks')
    await settle(() => {
      expect(wrapper.find('.page').exists()).toBe(false)
      expect(wrapper.find('.split').exists()).toBe(true)
      expect(wrapper.get('main').attributes('data-page-width')).toBe('full')
    })
    await router.push('/settings/appearance')
    await settle(() => expect(wrapper.get('main').attributes('data-page-width')).toBe('full'))
  })

  it('shows NotFound for unknown addresses', async () => {
    const { wrapper } = await open('/no/such/page')
    await settle(() => expect(wrapper.text()).toContain('Page not found'))
  })

  it('opens settings over the current page and keeps that page rendered', async () => {
    const { wrapper, router } = await open('/tasks')
    await router.push('/settings/language')
    await settle(() => {
      expect(wrapper.get('h1').text()).toBe('Tasks')
      expect(document.querySelector('[role=dialog]')?.textContent).toContain('Browser default')
      expect(document.getElementById('settings-section-title')?.textContent?.trim()).toBe(
        'Language',
      )
    })
  })

  it('renders Watch behind settings on a cold open', async () => {
    const { wrapper } = await open('/settings/appearance')
    await settle(() => {
      expect(wrapper.get('h1').text()).toBe('Watch')
      expect(document.querySelectorAll('[data-theme-preview]').length).toBeGreaterThan(26)
    })
  })

  it('picks a file icon set below the theme, with previews drawn in each set, and saves it', async () => {
    await open('/settings/appearance')
    await settle(() => expect(document.querySelectorAll('[data-icon-set]')).toHaveLength(7))
    const rows = [...document.querySelectorAll('[data-setting]')].map((row) =>
      row.getAttribute('data-setting'),
    )
    expect(rows.slice(0, 2)).toEqual(['theme', 'fileIcons'])
    const cards = [...document.querySelectorAll<HTMLElement>('[data-icon-set]')]
    expect(cards.map((card) => card.dataset.iconSet)).toEqual([
      'seelegent',
      'seelegent-mono',
      'seti',
      'material',
      'jetbrains',
      'catppuccin',
      'off',
    ])
    const seti = cards[2] as HTMLElement
    expect(seti.textContent).toContain('MIT')
    expect(seti.querySelector('img.fi')?.getAttribute('src')).toBe('/fileicons/seti/folder.svg')
    expect(cards[6]?.querySelectorAll('.fi')).toHaveLength(0)
    seti.querySelector('input')?.click()
    await flushPromises()
    expect(useUiStore().fileIcons).toBe('seti')
    expect(localStorage.getItem('seelegent.v1.ui.fileIcons')).toBe('"seti"')
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
