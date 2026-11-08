import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { AccountsApi } from '@/api/accounts'
import { ApiError } from '@/api/client'
import type { ProvidersApi } from '@/api/providers'
import type { KeyTestResult, Provider, ProviderPreset } from '@/api/types'
import { useAccountsStore } from '@/stores/accounts'
import { useProvidersStore } from '@/stores/providers'
import { useToastStore } from '@/stores/toast'
import { setupApp } from '@/test/app'
import { makeAccount, makeProvider } from '@/test/fixtures'

import AddAccountDialog from './AddAccountDialog.vue'

const preset = (overrides: Partial<ProviderPreset>): ProviderPreset => ({
  id: 'x',
  name: 'X',
  backend: 'claude',
  baseUrl: '',
  enabled: true,
  models: [],
  official: false,
  ...overrides,
})

const PRESETS: ProviderPreset[] = [
  preset({
    id: 'anthropic',
    name: 'Anthropic',
    official: true,
    baseUrl: 'https://api.anthropic.com',
  }),
  preset({ id: 'deepseek', name: 'DeepSeek', baseUrl: 'https://api.deepseek.com/anthropic' }),
  preset({ id: 'openrouter', name: 'OpenRouter', baseUrl: 'https://openrouter.ai/api' }),
  preset({ id: 'custom-anthropic', name: 'Custom (Anthropic)' }),
]

const works: KeyTestResult = {
  ok: true,
  status: 200,
  reason: 'ok',
  message: 'ok',
  models: ['deepseek-chat', 'deepseek-reasoner'],
  latencyMs: 80,
}
const refused: KeyTestResult = { ...works, ok: false, status: 401, reason: 'refused', models: [] }

const keyAccount = makeAccount({
  id: 'k1',
  label: 'DeepSeek 1',
  kind: 'api-key',
  backend: 'claude',
  provider: 'deepseek',
  status: 'ok',
})

interface Setup {
  accounts?: Partial<AccountsApi>
  test?: ProvidersApi['test']
  items?: Provider[]
  props?: { service?: string }
}

async function mountDialog({ accounts = {}, test, items = [], props = {} }: Setup = {}) {
  const { plugins } = await setupApp()
  useAccountsStore().setApi(accounts as AccountsApi)
  useProvidersStore().setApi({
    list: () => Promise.resolve({ items, presets: PRESETS }),
    test: test ?? vi.fn(() => Promise.reject(new Error('unexpected test'))),
  } as unknown as ProvidersApi)
  await useProvidersStore().ensure()
  const wrapper = mount(AddAccountDialog, {
    props: { open: false, ...props },
    global: { plugins },
    attachTo: document.body,
  })
  await wrapper.setProps({ open: true })
  await flushPromises()
  return wrapper
}

const body = () => document.body
const text = () => body().textContent ?? ''
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
const keyInput = () => body().querySelector<HTMLInputElement>('.keyfield input') as HTMLInputElement
function type(input: HTMLInputElement, value: string) {
  input.value = value
  input.dispatchEvent(new Event('input'))
}
/** A click from a pointer: detail 1, like a real mouse click (a key press makes clicks with detail 0). */
function pointerClick(el: Element) {
  el.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, detail: 1 }))
}
const tick = () => new Promise((resolve) => setTimeout(resolve))

async function toKeyStep() {
  pointerClick(radio('apiKey').closest('label') as HTMLLabelElement)
  await tick()
  await flushPromises()
}

afterEach(() => {
  document.body.innerHTML = ''
})

describe('AddAccountDialog', () => {
  it('asks first how the account connects, with real radios; a pointer click goes on at once', async () => {
    const wrapper = await mountDialog()
    expect(radio('subscription').checked).toBe(true)
    expect(radio('apiKey').checked).toBe(false)
    expect(text()).toContain('Sign in with a subscription')
    expect(text()).toContain('Use an API key')
    await toKeyStep()
    expect(keyInput()).toBeTruthy()
    expect(text()).toContain('Test and add')
    wrapper.unmount()
  })

  it('moves by keyboard without skipping the step: the choice changes, Next goes on', async () => {
    const wrapper = await mountDialog()
    radio('apiKey').click() // a key press's click: detail 0
    await tick()
    await flushPromises()
    expect(keyInput()).toBeNull()
    button('Next').click()
    await flushPromises()
    expect(keyInput()).toBeTruthy()
    button('Back').click()
    await flushPromises()
    expect(radio('apiKey').checked).toBe(true)
    wrapper.unmount()
  })

  it('names the service of a pasted key while none was chosen', async () => {
    const wrapper = await mountDialog()
    await toKeyStep()
    type(keyInput(), 'sk-ant-api03-abcdefghijklmnop')
    await flushPromises()
    expect(text()).toContain('Detected Anthropic key')
    expect(text()).toContain('Runs through Claude Code')
    wrapper.unmount()
  })

  it('keeps a key with a web address in it from the daemon and says why', async () => {
    const test = vi.fn()
    const create = vi.fn()
    const wrapper = await mountDialog({ accounts: { create }, test })
    await toKeyStep()
    radio('preset:deepseek').checked = true
    radio('preset:deepseek').dispatchEvent(new Event('change'))
    type(keyInput(), 'https://api.deepseek.com/sk-123456789')
    await flushPromises()
    expect(text()).toContain('This looks like a web address, not a key.')
    expect(button('Test and add').disabled).toBe(true)
    button('Test and add').click()
    await flushPromises()
    expect(test).not.toHaveBeenCalled()
    expect(create).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('tests the key, then adds the account in one call and records the test on it', async () => {
    const test = vi.fn<ProvidersApi['test']>(() => Promise.resolve(works))
    const create = vi.fn<AccountsApi['create']>(() =>
      Promise.resolve({
        ...keyAccount,
        notices: [{ code: 'key_name_removed', message: 'removed' }],
      }),
    )
    const wrapper = await mountDialog({ accounts: { create }, test })
    await toKeyStep()
    radio('preset:deepseek').checked = true
    radio('preset:deepseek').dispatchEvent(new Event('change'))
    type(keyInput(), 'DEEPSEEK_API_KEY=sk-0123456789abcdef')
    await flushPromises()
    expect(text()).toContain('Removed DEEPSEEK_API_KEY= from the start')
    button('Test and add').click()
    await flushPromises()

    expect(test).toHaveBeenNthCalledWith(1, {
      newProvider: { preset: 'deepseek', backend: 'claude' },
      apiKey: 'sk-0123456789abcdef',
    })
    expect(create).toHaveBeenCalledTimes(1)
    expect(create).toHaveBeenCalledWith(
      expect.objectContaining({
        kind: 'api-key',
        apiKey: 'sk-0123456789abcdef',
        newProvider: { preset: 'deepseek', backend: 'claude' },
        label: 'DeepSeek 1',
      }),
    )
    expect(test).toHaveBeenNthCalledWith(2, { accountId: 'k1' })
    expect(wrapper.emitted('update:open')?.at(-1)).toEqual([false])
    expect(
      useToastStore()
        .toasts.map((t) => t.message)
        .join(' '),
    ).toContain('Added DeepSeek 1')
    wrapper.unmount()
  })

  it('adds nothing when the key is refused, says so, and offers to add it untested', async () => {
    const test = vi.fn<ProvidersApi['test']>(() => Promise.resolve(refused))
    const create = vi.fn<AccountsApi['create']>(() => Promise.resolve(keyAccount))
    const wrapper = await mountDialog({ accounts: { create }, test })
    await toKeyStep()
    radio('preset:deepseek').checked = true
    radio('preset:deepseek').dispatchEvent(new Event('change'))
    type(keyInput(), 'sk-0123456789abcdef')
    await flushPromises()
    button('Test and add').click()
    await flushPromises()

    expect(create).not.toHaveBeenCalled()
    expect(text()).toContain('The service refused this key.')
    button('Add without testing').click()
    await flushPromises()
    expect(create).toHaveBeenCalledTimes(1)
    expect(test).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('reuses the chosen own service instead of making another', async () => {
    const deepseek = makeProvider({ id: 'deepseek', name: 'DeepSeek', preset: 'deepseek' })
    const test = vi.fn<ProvidersApi['test']>(() => Promise.resolve(works))
    const create = vi.fn<AccountsApi['create']>(() => Promise.resolve(keyAccount))
    const wrapper = await mountDialog({
      accounts: { create },
      test,
      items: [deepseek],
      props: { service: 'provider:deepseek' },
    })
    // "Add key" on a service opens the form with it chosen.
    expect(keyInput()).toBeTruthy()
    expect(text()).toContain('DeepSeek')
    expect(() => button('Back')).toThrow()
    type(keyInput(), 'sk-0123456789abcdef')
    await flushPromises()
    button('Test and add').click()
    await flushPromises()
    const sent = create.mock.calls[0]?.[0]
    expect(sent?.provider).toBe('deepseek')
    expect(sent?.newProvider).toBeUndefined()
    wrapper.unmount()
  })

  it('shows the field the daemon refused at that field', async () => {
    const create = vi.fn<AccountsApi['create']>(() =>
      Promise.reject(
        new ApiError(400, 'invalid_argument', 'bad', {
          fields: [{ field: 'apiKey', message: 'key too short' }],
        }),
      ),
    )
    const wrapper = await mountDialog({ accounts: { create } })
    await toKeyStep()
    radio('preset:deepseek').checked = true
    radio('preset:deepseek').dispatchEvent(new Event('change'))
    type(keyInput(), 'sk-0123456789abcdef')
    await flushPromises()
    await wrapper.findComponent({ name: 'ApiKeyForm' }).vm.submit(false)
    await flushPromises()
    expect(text()).toContain('key too short')
    wrapper.unmount()
  })

  it('offers this computer’s Antigravity sign-in only while no account uses it', async () => {
    const wrapper = await mountDialog()
    button('Next').click()
    await flushPromises()
    expect(text()).toContain('Use the sign-in already on this computer')
    useAccountsStore().replaceAll([
      makeAccount({ id: 's1', label: 'Gemini here', kind: 'system', backend: 'agy' }),
    ])
    await flushPromises()
    expect(text()).not.toContain('Use the sign-in already on this computer')
    wrapper.unmount()
  })

  it('says when the sign-in of the new account could not start (E-B11)', async () => {
    const isolated = makeAccount({ id: 'g1', label: 'Gemini 1', kind: 'isolated', backend: 'agy' })
    const api = {
      create: vi.fn(() => Promise.resolve(isolated)),
      login: vi.fn(() => Promise.reject(new ApiError(500, 'internal', 'no terminal'))),
    }
    const wrapper = await mountDialog({ accounts: api })
    button('Next').click()
    await flushPromises()
    button('Sign in').click()
    await flushPromises()

    expect(api.create).toHaveBeenCalledWith({
      backend: 'agy',
      kind: 'isolated',
      label: 'Antigravity 1',
    })
    expect(api.login).toHaveBeenCalledWith('g1')
    const failed = useToastStore().toasts.find((t) => t.tone === 'error')
    expect(failed?.message).toBe(
      'Gemini 1 was added, but its sign-in could not start: no terminal. Use Sign in on its card to try again.',
    )
    expect(text()).toContain('Gemini 1 was added. Finish its sign-in below')
    expect(button('Done')).toBeTruthy()
    wrapper.unmount()
  })
})
