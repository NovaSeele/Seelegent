import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/api/client'
import type { ProvidersApi } from '@/api/providers'
import type { PriceTable, Provider } from '@/api/types'
import { useProvidersStore } from '@/stores/providers'
import { useToastStore } from '@/stores/toast'
import { setupApp } from '@/test/app'
import { makeProvider } from '@/test/fixtures'

import PricesDialog from './PricesDialog.vue'

afterEach(() => {
  document.body.innerHTML = ''
})

const DEEPSEEK = makeProvider({
  id: 'deepseek',
  name: 'DeepSeek',
  pricingUrl: 'https://api-docs.deepseek.com/quick_start/pricing',
  spendSource: 'prices',
  prices: [{ model: 'deepseek-chat', inputUsd: 0.27, cachedInputUsd: 0.07, outputUsd: 1.1 }],
})

async function open(provider: Provider, setPrices: ProvidersApi['setPrices']) {
  const { plugins } = await setupApp()
  const store = useProvidersStore()
  store.setApi({ setPrices } as unknown as ProvidersApi)
  store.items = [provider]
  const wrapper = mount(PricesDialog, {
    props: { open: false, provider },
    global: { plugins },
    attachTo: document.body,
  })
  await wrapper.setProps({ open: true })
  await flushPromises()
  return wrapper
}

const inputs = (label: string) =>
  [...document.body.querySelectorAll<HTMLInputElement>('.prices input')].filter(
    (i) => document.querySelector(`label[for="${i.id}"]`)?.textContent?.trim() === label,
  )
const type = (input: HTMLInputElement | undefined, value: string) => {
  if (!input) throw new Error('no input')
  input.value = value
  input.dispatchEvent(new Event('input'))
}
const button = (label: string) => {
  const found = [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(
    (b) => b.textContent?.trim() === label || b.getAttribute('aria-label') === label,
  )
  if (!found) throw new Error(`no button "${label}"`)
  return found
}
const text = () => document.body.textContent ?? ''
const table = (provider: Provider, prices: PriceTable['prices']): PriceTable => ({
  provider: provider.id,
  prices,
  spendSource: prices.length ? 'prices' : 'none',
})

describe('PricesDialog', () => {
  it('starts from the saved table, adds a row and saves the whole table', async () => {
    const setPrices = vi.fn<ProvidersApi['setPrices']>((_, body) =>
      Promise.resolve(table(DEEPSEEK, body.prices)),
    )
    await open(DEEPSEEK, setPrices)
    expect(inputs('Model')[0]?.value).toBe('deepseek-chat')
    const link = document.querySelector<HTMLAnchorElement>('.prices__link')
    expect(link?.href).toBe(DEEPSEEK.pricingUrl)
    expect(link?.textContent?.trim()).toBe("DeepSeek's price page")
    button('Add a row').click()
    await flushPromises()
    expect(document.activeElement).toBe(inputs('Model')[1])
    type(inputs('Model')[1], '*')
    type(inputs('Input')[1], '1,5')
    type(inputs('Output')[1], '2')
    await flushPromises()
    button('Save prices').click()
    await flushPromises()
    expect(setPrices).toHaveBeenCalledWith('deepseek', {
      prices: [
        { model: 'deepseek-chat', inputUsd: 0.27, cachedInputUsd: 0.07, outputUsd: 1.1 },
        { model: '*', inputUsd: 1.5, outputUsd: 2 },
      ],
    })
    expect(useProvidersStore().items[0]?.prices).toHaveLength(2)
    expect(useToastStore().toasts.map((t) => t.message)).toEqual(['Prices of DeepSeek saved'])
  })

  it('shows its own checks at the field and sends nothing', async () => {
    const setPrices = vi.fn<ProvidersApi['setPrices']>()
    await open(DEEPSEEK, setPrices)
    button('Add a row').click()
    await flushPromises()
    type(inputs('Model')[1], 'DEEPSEEK-CHAT')
    type(inputs('Input')[1], '20000')
    await flushPromises()
    button('Save prices').click()
    await flushPromises()
    expect(setPrices).not.toHaveBeenCalled()
    expect(text()).toContain('This model has a row already')
    expect(text()).toContain('Enter a price from 0 to 10,000')
    expect(inputs('Input')[1]?.getAttribute('aria-invalid')).toBe('true')
  })

  it('puts the daemon’s field errors on their row and column', async () => {
    const setPrices = vi.fn<ProvidersApi['setPrices']>(() =>
      Promise.reject(
        new ApiError(400, 'invalid_argument', 'bad', {
          fields: [{ field: 'prices[0].outputUsd', message: 'too high for this model' }],
        }),
      ),
    )
    await open(DEEPSEEK, setPrices)
    button('Save prices').click()
    await flushPromises()
    expect(text()).toContain('too high for this model')
    expect(inputs('Output')[0]?.getAttribute('aria-invalid')).toBe('true')
  })

  it('clears the prices when every row is removed', async () => {
    const setPrices = vi.fn<ProvidersApi['setPrices']>(() => Promise.resolve(table(DEEPSEEK, [])))
    await open(DEEPSEEK, setPrices)
    button('Remove the row of deepseek-chat').click()
    await flushPromises()
    expect(document.activeElement?.textContent?.trim()).toBe('Add a row')
    expect(text()).toContain('No prices')
    button('Save prices').click()
    await flushPromises()
    expect(setPrices).toHaveBeenCalledWith('deepseek', { prices: [] })
    expect(useToastStore().toasts.map((t) => t.message)).toEqual(['Prices of DeepSeek removed'])
  })
})
