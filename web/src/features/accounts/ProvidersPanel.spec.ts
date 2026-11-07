import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { ProvidersApi } from '@/api/providers'
import { useAccountsStore } from '@/stores/accounts'
import { useProvidersStore } from '@/stores/providers'
import { setupApp } from '@/test/app'
import { makeAccount, makeProvider } from '@/test/fixtures'

import ProvidersPanel from './ProvidersPanel.vue'

afterEach(() => {
  document.body.innerHTML = ''
})

// Live run against the daemon: Provider.accounts stayed 0 after an account was added (no providers.changed for
// account writes), and a provider saved with models showed "any model" (models [] with the list in
// allowedModels). Review: the line follows the allow-list rules (a list, a hand-written pattern, or any model
// when both are empty; tier models answered in models never count as the list).
describe('ProvidersPanel', () => {
  it('counts accounts from the live account list and shows the allowed models', async () => {
    const { plugins } = await setupApp()
    useProvidersStore().setApi({
      list: vi.fn(() =>
        Promise.resolve({
          items: [
            makeProvider({
              id: 'anthropic',
              name: 'Anthropic',
              official: true,
              baseUrl: '',
              accounts: 0,
              allowedModels: '^(?:claude-sonnet-5|claude-opus-5)$',
            }),
            makeProvider({ id: 'zai', name: 'Z.ai', allowedModels: '^glm-' }),
            makeProvider({
              id: 'deepseek',
              name: 'DeepSeek',
              models: ['deepseek-chat'],
              tiers: { fast: 'deepseek-chat', standard: '', strong: '', max: '' },
            }),
          ],
          presets: [],
        }),
      ),
    } as unknown as ProvidersApi)
    const accounts = useAccountsStore()
    accounts.replaceAll([
      makeAccount({ id: 'k1', kind: 'api-key', backend: 'claude', provider: 'anthropic' }),
    ])
    const wrapper = mount(ProvidersPanel, { global: { plugins }, attachTo: document.body })
    await flushPromises()

    const rows = () => wrapper.findAll('.prov__meta').map((row) => row.text())
    expect(rows()).toEqual([
      'used by 1 account · claude-sonnet-5, claude-opus-5',
      'used by 0 accounts · pattern ^glm-',
      'used by 0 accounts · any model',
    ])

    accounts.upsert(makeAccount({ id: 'k2', kind: 'api-key', backend: 'claude', provider: 'zai' }))
    await flushPromises()
    expect(rows()[1]).toBe('used by 1 account · pattern ^glm-')
    wrapper.unmount()
  })
})
