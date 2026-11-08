import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { ProvidersApi } from '@/api/providers'
import type { Provider, ProviderPreset } from '@/api/types'
import { useProvidersStore } from '@/stores/providers'
import { setupApp } from '@/test/app'
import { makeProvider } from '@/test/fixtures'

import ProviderDialog from './ProviderDialog.vue'

const presets: ProviderPreset[] = [
  {
    id: 'anthropic',
    name: 'Anthropic',
    backend: 'claude',
    baseUrl: '',
    enabled: true,
    models: [],
    official: true,
  },
  {
    id: 'groq',
    name: 'Groq',
    backend: 'codex',
    baseUrl: 'https://api.groq.com/openai/v1',
    enabled: true,
    models: [],
    wireApi: 'chat',
    official: false,
  },
]

async function mountDialog(provider: Provider | null = null, update = vi.fn()) {
  const { plugins } = await setupApp()
  const store = useProvidersStore()
  store.setApi({
    list: vi.fn(() => Promise.resolve({ items: provider ? [provider] : [], presets })),
    update,
  } as unknown as ProvidersApi)
  await store.load()
  const wrapper = mount(ProviderDialog, {
    props: { open: false, provider },
    global: { plugins },
    attachTo: document.body,
  })
  await wrapper.setProps({ open: true })
  await flushPromises()
  return wrapper
}

const field = (label: string) =>
  [...document.body.querySelectorAll<HTMLInputElement | HTMLSelectElement>('input, select')].find(
    (el) => el.labels?.[0]?.textContent?.trim() === label,
  )

async function pick(preset: string) {
  const select = field('Start from') as HTMLSelectElement
  select.value = preset
  select.dispatchEvent(new Event('change'))
  await flushPromises()
}

afterEach(() => {
  document.body.innerHTML = ''
})

// Live run against the daemon: the official preset's base URL is always cleared, and a preset refuses another
// backend with a 400; the form no longer offers either.
describe('ProviderDialog', () => {
  it('hides the base URL of the official provider and locks the backend of a preset', async () => {
    const wrapper = await mountDialog()
    expect(field('Base URL')).toBeDefined()
    expect(field('Backend')?.disabled).toBe(false)

    await pick('anthropic')
    expect(field('Base URL')).toBeUndefined()
    expect(field('Backend')?.disabled).toBe(true)

    await pick('groq')
    expect((field('Base URL') as HTMLInputElement).value).toBe('https://api.groq.com/openai/v1')
    expect(field('Backend')?.value).toBe('codex')
    expect(field('Backend')?.disabled).toBe(true)

    await pick('')
    expect(field('Backend')?.disabled).toBe(false)
    wrapper.unmount()
  })

  // Review S1: the dialog filled the models field with the tier model the daemon answered in `models`, so adding
  // a model and saving replaced the real allow-list (deepseek-v4 and deepseek-r2 lost access).
  it('edits the allow-list only, and the tier models apart', async () => {
    const update = vi.fn((_id: string, changes: object) =>
      Promise.resolve(makeProvider({ id: 'deepseek', ...changes })),
    )
    const deepseek = makeProvider({
      id: 'deepseek',
      name: 'DeepSeek',
      models: ['deepseek-chat'],
      allowedModels: '^(?:deepseek-v4|deepseek-r2)$',
      tiers: { fast: 'deepseek-chat', standard: '', strong: '', max: '' },
    })
    const wrapper = await mountDialog(deepseek, update)
    const models = field('Models') as HTMLInputElement
    expect(models.value).toBe('deepseek-v4, deepseek-r2')
    expect((field('Fast') as HTMLInputElement).value).toBe('deepseek-chat')

    models.value = 'deepseek-v4, deepseek-r2, deepseek-v5'
    models.dispatchEvent(new Event('input'))
    document.body.querySelector<HTMLFormElement>('#provider-form')?.requestSubmit()
    await flushPromises()
    expect(update).toHaveBeenCalledWith('deepseek', {
      models: ['deepseek-v4', 'deepseek-r2', 'deepseek-v5'],
    })
    wrapper.unmount()
  })

  // Design trap 4: no UI could set a provider's tier models, so tasks without a model failed.
  it('saves the tier models the user sets, and only them', async () => {
    const update = vi.fn((_id: string, changes: object) =>
      Promise.resolve(makeProvider({ id: 'zai', ...changes })),
    )
    const wrapper = await mountDialog(makeProvider({ id: 'zai', name: 'Z.ai' }), update)
    for (const tier of ['Fast', 'Standard', 'Strong', 'Max']) {
      const input = field(tier) as HTMLInputElement
      input.value = 'glm-5'
      input.dispatchEvent(new Event('input'))
    }
    document.body.querySelector<HTMLFormElement>('#provider-form')?.requestSubmit()
    await flushPromises()
    expect(update).toHaveBeenCalledWith('zai', {
      tiers: { fast: 'glm-5', standard: 'glm-5', strong: 'glm-5', max: 'glm-5' },
    })
    wrapper.unmount()
  })

  // Review G1: a hand-written pattern is never replaced silently.
  it('names a hand-written pattern and says a list replaces it', async () => {
    const zai = makeProvider({ name: 'Z.ai', allowedModels: '^glm-' })
    const wrapper = await mountDialog(zai)
    expect((field('Models') as HTMLInputElement).value).toBe('')
    expect(document.body.textContent).toContain(
      'Allowed by pattern ^glm-; entering a list replaces this pattern.',
    )
    wrapper.unmount()
  })

  it('shows any model when neither a list nor a pattern is set', async () => {
    const wrapper = await mountDialog(makeProvider({ name: 'Z.ai' }))
    const models = field('Models') as HTMLInputElement
    expect(models.value).toBe('')
    expect(models.placeholder).toBe('any model')
    expect(document.body.textContent).toContain('Separated by commas; empty allows any model.')
    expect(document.body.textContent).not.toContain('Always allowed')
    wrapper.unmount()
  })

  // Review G3: an empty field means "unchanged", so a hand-written pattern could not go back to any model.
  it('turns a hand-written pattern into any model with the hint button', async () => {
    const update = vi.fn((_id: string, changes: object) =>
      Promise.resolve(makeProvider({ id: 'zai', ...changes })),
    )
    const zai = makeProvider({ id: 'zai', name: 'Z.ai', allowedModels: '^glm-' })
    const wrapper = await mountDialog(zai, update)
    const button = () =>
      [...document.body.querySelectorAll('button')].find(
        (b) => b.textContent?.trim() === 'Allow any model',
      )
    const allow = button()
    expect(allow?.textContent?.trim()).toBe('Allow any model')
    expect(allow?.type).toBe('button')
    expect(allow?.getAttribute('aria-pressed')).toBe('false')

    allow?.focus()
    allow?.click()
    await flushPromises()
    // A toggle keeps its label (WAI-ARIA APG): aria-pressed carries the state, and focus stays on it.
    expect(document.activeElement).toBe(allow)
    expect(button()).toBe(allow)
    expect(allow?.textContent?.trim()).toBe('Allow any model')
    expect(allow?.getAttribute('aria-pressed')).toBe('true')
    expect(document.body.textContent).toContain('Any model is allowed once you save')
    expect((field('Models') as HTMLInputElement).placeholder).toBe('any model')

    document.body.querySelector<HTMLFormElement>('#provider-form')?.requestSubmit()
    await flushPromises()
    expect(update).toHaveBeenCalledWith('zai', { models: [] })
    wrapper.unmount()
  })

  // Review: the button sat inside the hint the input points to, so screen readers read its label as part of the
  // field's description.
  it('keeps the button out of the text the models field is described by', async () => {
    const zai = makeProvider({ id: 'zai', name: 'Z.ai', allowedModels: '^glm-' })
    const wrapper = await mountDialog(zai)
    const ids = (field('Models') as HTMLInputElement).getAttribute('aria-describedby') ?? ''
    const described = ids
      .split(' ')
      .filter(Boolean)
      .map((id) => document.getElementById(id))
    expect(described.length).toBeGreaterThan(0)
    for (const el of described) {
      expect(el).not.toBeNull()
      expect(el?.querySelector('button')).toBeNull()
      expect(el?.textContent).not.toContain('Allow any model')
    }
    expect(described.map((el) => el?.textContent?.trim())).toContain(
      'Allowed by pattern ^glm-; entering a list replaces this pattern.',
    )
    const toggle = [...document.body.querySelectorAll('button')].find(
      (b) => b.textContent?.trim() === 'Allow any model',
    )
    expect(toggle).toBeDefined()
    wrapper.unmount()
  })

  it('keeps the pattern when the button is clicked back, and hides it while a list is typed', async () => {
    const update = vi.fn((_id: string, changes: object) =>
      Promise.resolve(makeProvider({ id: 'zai', ...changes })),
    )
    const zai = makeProvider({ id: 'zai', name: 'Z.ai', allowedModels: '^glm-' })
    const wrapper = await mountDialog(zai, update)
    const find = (label: string) =>
      [...document.body.querySelectorAll('button')].find((b) => b.textContent?.trim() === label)
    find('Allow any model')?.click()
    await flushPromises()
    expect(find('Allow any model')?.getAttribute('aria-pressed')).toBe('true')
    find('Allow any model')?.click()
    await flushPromises()
    expect(find('Allow any model')?.getAttribute('aria-pressed')).toBe('false')
    expect(document.body.textContent).toContain('Allowed by pattern ^glm-')

    const models = field('Models') as HTMLInputElement
    models.value = 'glm-5'
    models.dispatchEvent(new Event('input'))
    await flushPromises()
    expect(find('Allow any model')).toBeUndefined()

    models.value = ''
    models.dispatchEvent(new Event('input'))
    document.body.querySelector<HTMLFormElement>('#provider-form')?.requestSubmit()
    await flushPromises()
    expect(update).toHaveBeenCalledWith('zai', {})
    wrapper.unmount()
  })
})
