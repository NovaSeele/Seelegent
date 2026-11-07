import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { ProvidersApi } from '@/api/providers'
import type { ProviderPreset } from '@/api/types'
import { useProvidersStore } from '@/stores/providers'
import { setupApp } from '@/test/app'

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

async function mountDialog() {
  const { plugins } = await setupApp()
  const store = useProvidersStore()
  store.setApi({
    list: vi.fn(() => Promise.resolve({ items: [], presets })),
  } as unknown as ProvidersApi)
  await store.load()
  const wrapper = mount(ProviderDialog, {
    props: { open: false, provider: null },
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
})
