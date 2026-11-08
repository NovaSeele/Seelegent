/** Typed endpoints of API providers (contract §4). */
import type { ApiClient } from './client'
import type {
  KeyTestRequest,
  KeyTestResult,
  PricesRequest,
  PriceTable,
  Provider,
  ProviderInput,
  ProviderList,
} from './types'

const enc = encodeURIComponent

/** PATCH /providers/{id} (a ProviderInput): id, backend and preset cannot change, accounts is ignored. */
export type ProviderChanges = Omit<ProviderInput, 'id' | 'backend' | 'preset' | 'accounts'>

export function createProvidersApi(client: ApiClient) {
  return {
    list: (signal?: AbortSignal) => client.get<ProviderList>('/providers', { signal }),
    create: (body: ProviderInput) => client.post<Provider>('/providers', { body }),
    update: (id: string, body: ProviderChanges) =>
      client.patch<Provider>(`/providers/${enc(id)}`, { body }),
    /** 409 conflict while accounts use it. */
    remove: (id: string) => client.delete<void>(`/providers/${enc(id)}`),
    /**
     * Checks a key with one call that spends no tokens (the service's model list). 200 also when the key is
     * refused; with accountId the stored key is used and the result is saved on the account.
     */
    test: (body: KeyTestRequest) => client.post<KeyTestResult>('/providers/test', { body }),
    /** The provider's price table (USD per 1M tokens). */
    prices: (id: string) => client.get<PriceTable>(`/providers/${enc(id)}/prices`),
    /** Replaces the whole price table; [] removes every price. 400 with details.field prices[i].<field>. */
    setPrices: (id: string, body: PricesRequest) =>
      client.put<PriceTable>(`/providers/${enc(id)}/prices`, { body }),
  }
}

export type ProvidersApi = ReturnType<typeof createProvidersApi>
