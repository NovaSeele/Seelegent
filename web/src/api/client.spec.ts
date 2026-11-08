import { describe, expect, it, vi } from 'vitest'

import { ApiError, createApiClient, NetworkError } from './client'

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function setup(response: Response | Error | DOMException) {
  const fetch = vi.fn<typeof globalThis.fetch>(async () => {
    if (!(response instanceof Response)) throw response
    return response
  })
  const client = createApiClient({ fetch, getLocale: () => 'vi' })
  const call = () => fetch.mock.calls[0] as [string, RequestInit]
  const headers = () => call()[1].headers as Record<string, string>
  return { client, fetch, call, headers }
}

describe('api client', () => {
  it('GETs JSON under /api/v1 with Accept-Language and without the CSRF header', async () => {
    const { client, call, headers } = setup(json({ status: 'ok' }))
    await expect(
      client.get('/health', { query: { a: 1, skip: undefined, b: 'x y' } }),
    ).resolves.toEqual({
      status: 'ok',
    })
    expect(call()[0]).toBe('/api/v1/health?a=1&b=x+y')
    expect(call()[1].method).toBe('GET')
    expect(headers()['Accept-Language']).toBe('vi')
    expect(headers()['X-Seelegent']).toBeUndefined()
  })

  it('sends X-Seelegent: 1 and a JSON body on mutations', async () => {
    for (const method of ['post', 'put', 'patch', 'delete'] as const) {
      const { client, call, headers } = setup(new Response(null, { status: 204 }))
      await expect(client[method]('/tasks/1', { body: { a: 1 } })).resolves.toBeUndefined()
      expect(call()[1].method).toBe(method.toUpperCase())
      expect(headers()['X-Seelegent']).toBe('1')
      expect(headers()['Content-Type']).toBe('application/json')
      expect(call()[1].body).toBe('{"a":1}')
    }
  })

  it('turns error responses into ApiError with the daemon code and message', async () => {
    const { client } = setup(json({ error: { code: 'not_found', message: 'No such task' } }, 404))
    const error = await client.get('/tasks/x').catch((e: unknown) => e)
    expect(error).toBeInstanceOf(ApiError)
    expect(error).toMatchObject({ status: 404, code: 'not_found', message: 'No such task' })
  })

  it('falls back to the status when the error body is not JSON', async () => {
    const { client } = setup(new Response('oops', { status: 502, statusText: 'Bad Gateway' }))
    await expect(client.get('/health')).rejects.toMatchObject({ status: 502, code: 'http_502' })
  })

  it('reports an unreachable daemon as NetworkError and passes aborts through', async () => {
    const { client } = setup(new TypeError('Failed to fetch'))
    await expect(client.get('/health')).rejects.toBeInstanceOf(NetworkError)
    const abort = setup(new DOMException('aborted', 'AbortError'))
    await expect(abort.client.get('/health')).rejects.toMatchObject({ name: 'AbortError' })
  })
})
