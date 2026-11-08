import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/api/client'
import type { ProvidersApi } from '@/api/providers'
import type { Provider } from '@/api/types'
import { useAccountsStore } from '@/stores/accounts'
import { useProvidersStore } from '@/stores/providers'
import { setupApp } from '@/test/app'
import { makeAccount, makeProvider } from '@/test/fixtures'

import ServicesSection from './ServicesSection.vue'

afterEach(() => {
  document.body.innerHTML = ''
})

const ITEMS: Provider[] = [
  makeProvider({
    id: 'anthropic',
    name: 'Anthropic',
    official: true,
    baseUrl: '',
    preset: 'anthropic',
    allowedModels: '^(?:claude-sonnet-5|claude-opus-5)$',
  }),
  makeProvider({ id: 'zai', name: 'Z.ai', allowedModels: '^glm-' }),
  makeProvider({
    id: 'deepseek',
    name: 'DeepSeek',
    preset: 'deepseek',
    baseUrl: 'https://api.deepseek.com/anthropic',
    models: ['deepseek-chat'],
    tiers: { fast: 'deepseek-chat', standard: '', strong: '', max: '' },
  }),
]

async function mountSection(api: Partial<ProvidersApi> = {}, props = {}) {
  const { plugins } = await setupApp()
  useProvidersStore().setApi({
    list: vi.fn(() => Promise.resolve({ items: ITEMS, presets: [] })),
    ...api,
  } as unknown as ProvidersApi)
  const accounts = useAccountsStore()
  accounts.replaceAll([
    makeAccount({
      id: 'k1',
      label: 'Work key',
      kind: 'api-key',
      backend: 'claude',
      provider: 'anthropic',
    }),
  ])
  const wrapper = mount(ServicesSection, { props, global: { plugins }, attachTo: document.body })
  await flushPromises()
  return { wrapper, accounts }
}

const text = () => document.body.textContent ?? ''
const button = (label: string) => {
  const found = [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(
    (b) => b.textContent?.trim() === label || b.getAttribute('aria-label') === label,
  )
  if (!found) throw new Error(`no button "${label}"`)
  return found
}

// Live run against the daemon: Provider.accounts stayed 0 after an account was added (no providers.changed for
// account writes), so the key count comes from the live account list. The allow line follows the allow-list rules
// (a list, a hand-written pattern, or any model when both are empty; tier models never count as the list).
describe('ServicesSection', () => {
  it('starts collapsed under an Advanced label and opens on its title', async () => {
    const { wrapper } = await mountSection()
    const toggle = button('Services')
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    expect(text()).toContain('Advanced')
    expect(wrapper.findAll('.svc')).toHaveLength(0)
    toggle.click()
    await flushPromises()
    expect(toggle.getAttribute('aria-expanded')).toBe('true')
    expect(wrapper.findAll('.svc')).toHaveLength(3)
    wrapper.unmount()
  })

  it('shows address, models, key count or "No key yet", from the live account list', async () => {
    const { wrapper, accounts } = await mountSection({}, { collapsible: false })
    const meta = () =>
      wrapper.findAll('.svc__meta').map((row) =>
        row
          .findAll('span')
          .map((s) => s.text())
          .join(' · '),
      )
    expect(wrapper.find('.svc__url').text()).toBe('api.anthropic.com (official)')
    expect(meta()).toEqual([
      'Model chosen per task · allows claude-sonnet-5, claude-opus-5 · 1 key',
      'Model chosen per task · allows pattern ^glm- · No key yet',
      'Model: deepseek-chat · allows any model · No key yet',
    ])
    accounts.upsert(makeAccount({ id: 'k2', kind: 'api-key', backend: 'claude', provider: 'zai' }))
    await flushPromises()
    expect(meta()[1]).toContain('1 key')
    wrapper.unmount()
  })

  it('opens the API-key form with the service chosen from "Add key"', async () => {
    const { wrapper } = await mountSection({}, { collapsible: false })
    const addKey = wrapper
      .findAll('.svc')[2]
      ?.findAll('button')
      .find((b) => b.text() === 'Add key')
    await addKey?.trigger('click')
    await flushPromises()
    expect(document.body.querySelector('.keyfield input')).toBeTruthy()
    expect(document.querySelector('.picker__line')?.textContent).toContain('DeepSeek')
    wrapper.unmount()
  })

  it('opens the service’s prices from "Prices", and offers none for a local service', async () => {
    const { wrapper } = await mountSection({}, { collapsible: false })
    await wrapper.find('button[aria-label="Prices of DeepSeek"]').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('US dollars per 1M tokens')
    expect(document.body.querySelector('[role="dialog"]')?.textContent).toContain(
      'Prices of DeepSeek',
    )
    wrapper.unmount()
    const ollama = makeProvider({ id: 'ollama', name: 'Ollama', spendSource: 'free' })
    const local = await mountSection(
      { list: vi.fn(() => Promise.resolve({ items: [ollama], presets: [] })) },
      { collapsible: false },
    )
    expect(local.wrapper.find('.svc').exists()).toBe(true)
    expect(local.wrapper.find('button[aria-label="Prices of Ollama"]').exists()).toBe(false)
    local.wrapper.unmount()
  })

  it('names the accounts that still use a service, and shows the daemon’s refusal', async () => {
    const remove = vi.fn(() => Promise.reject(new ApiError(409, 'conflict', 'provider in use')))
    const { wrapper } = await mountSection({ remove }, { collapsible: false })
    button('Delete Anthropic').click()
    await flushPromises()
    expect(text()).toContain('Work key still uses it: remove that account first.')
    button('Delete service').click()
    await flushPromises()
    expect(remove).toHaveBeenCalledWith('anthropic')
    expect(text()).toContain('Accounts still use it; remove them first. provider in use')
    wrapper.unmount()
  })
})
