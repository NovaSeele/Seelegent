import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { AccountsApi } from '@/api/accounts'
import { ApiError } from '@/api/client'
import type { ProvidersApi } from '@/api/providers'
import { useAccountsStore } from '@/stores/accounts'
import { useProvidersStore } from '@/stores/providers'
import { setupApp } from '@/test/app'
import { makeAccount } from '@/test/fixtures'

import AddAccountDialog from './AddAccountDialog.vue'

const created = makeAccount({
  id: 'k1',
  label: 'Claude 1',
  kind: 'api-key',
  backend: 'claude',
  provider: 'anthropic',
  status: 'no_key',
})

async function mountDialog(api: Partial<AccountsApi>) {
  const { plugins } = await setupApp()
  useAccountsStore().setApi(api as AccountsApi)
  useProvidersStore().setApi({
    list: () =>
      Promise.resolve({
        items: [
          {
            id: 'anthropic',
            name: 'Anthropic',
            backend: 'claude',
            baseUrl: '',
            enabled: true,
            models: [],
            accounts: 0,
          },
        ],
        presets: [],
      }),
  } as unknown as ProvidersApi)
  const wrapper = mount(AddAccountDialog, {
    props: { open: false },
    global: { plugins },
    attachTo: document.body,
  })
  await wrapper.setProps({ open: true })
  await flushPromises()
  return wrapper
}

const body = () => document.body
const button = (label: string) => {
  const found = [...body().querySelectorAll<HTMLButtonElement>('button')].find(
    (b) => b.textContent?.trim() === label,
  )
  if (!found) throw new Error(`no button "${label}"`)
  return found
}
const radio = (value: string) => {
  const found = body().querySelector<HTMLInputElement>(`input[type="radio"][value="${value}"]`)
  if (!found) throw new Error(`no choice ${value}`)
  return found
}
function type(input: HTMLInputElement, value: string) {
  input.value = value
  input.dispatchEvent(new Event('input'))
}

afterEach(() => {
  document.body.innerHTML = ''
})

describe('AddAccountDialog', () => {
  it('blocks this computer’s sign-in only for the backend that has it, and says why', async () => {
    const wrapper = await mountDialog({})
    useAccountsStore().replaceAll([
      makeAccount({ id: 's1', label: 'Claude here', kind: 'system', backend: 'claude' }),
    ])
    await flushPromises()
    expect(radio('system').disabled).toBe(false)
    useAccountsStore().replaceAll([
      makeAccount({ id: 's2', label: 'Gemini here', kind: 'system', backend: 'agy' }),
    ])
    await flushPromises()
    expect(radio('system').disabled).toBe(true)
    expect(body().textContent).toContain('Already added: Gemini here')
    wrapper.unmount()
  })

  it('turns into setting the key when the account stays without one', async () => {
    const api = {
      create: vi.fn(() => Promise.resolve(created)),
      setKey: vi
        .fn<AccountsApi['setKey']>()
        .mockRejectedValueOnce(new ApiError(400, 'invalid_argument', 'key too short'))
        .mockResolvedValueOnce({ ...created, status: 'ok' }),
      remove: vi.fn(() => Promise.reject(new ApiError(500, 'internal', 'disk full'))),
    }
    const wrapper = await mountDialog(api)
    radio('claude').click()
    await flushPromises()
    radio('api-key').click()
    await flushPromises()
    button('Next').click()
    await flushPromises()
    type(body().querySelector('input[type="password"]') as HTMLInputElement, 'sk')
    button('Add account').click()
    await flushPromises()

    expect(body().textContent).toContain('Claude 1 was added, but it has no key')
    expect(body().textContent).toContain('key too short')
    // Only the key is asked for now, and saving it does not add a second account.
    expect(body().querySelectorAll('input:not([type="password"])').length).toBe(0)
    expect(() => button('Back')).toThrow()
    type(body().querySelector('input[type="password"]') as HTMLInputElement, 'sk-long-enough')
    button('Save key').click()
    await flushPromises()
    expect(api.create).toHaveBeenCalledTimes(1)
    expect(api.setKey).toHaveBeenLastCalledWith('k1', 'sk-long-enough')
    expect(wrapper.emitted('update:open')?.at(-1)).toEqual([false])
    wrapper.unmount()
  })
})
