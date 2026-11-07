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
  it('edits the allow-list only, and shows the tier models apart', async () => {
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
    expect(document.body.textContent).toContain('Always allowed (tier models): deepseek-chat')

    models.value = 'deepseek-v4, deepseek-r2, deepseek-v5'
    models.dispatchEvent(new Event('input'))
    document.body.querySelector<HTMLFormElement>('#provider-form')?.requestSubmit()
    await flushPromises()
    expect(update).toHaveBeenCalledWith('deepseek', {
      models: ['deepseek-v4', 'deepseek-r2', 'deepseek-v5'],
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
})
