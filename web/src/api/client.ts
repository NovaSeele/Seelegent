/**
 * Fetch wrapper for the daemon's HTTP API (/api/v1). Every request sends Accept-Language (the daemon localises
 * its messages); mutating requests also send `X-Seelegent: 1` (the daemon's CSRF check, together with a
 * same-origin Origin). Failures surface as typed errors:
 *   ApiError      the daemon answered with an error status ({ "error": { "code", "message" } })
 *   NetworkError  no answer (daemon down, connection refused, CORS)
 *   AbortError    the caller's AbortSignal fired (a DOMException, passed through unchanged)
 */
export class ApiError extends Error {
  override readonly name = 'ApiError'

  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message)
  }
}

export class NetworkError extends Error {
  override readonly name = 'NetworkError'

  constructor(
    message: string,
    override readonly cause?: unknown,
  ) {
    super(message)
  }
}

export type Method = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
export type Query = Record<string, string | number | boolean | null | undefined>

export interface RequestOptions {
  query?: Query
  body?: unknown
  signal?: AbortSignal
  headers?: Record<string, string>
}

export interface ApiClientOptions {
  baseUrl?: string
  /** BCP 47 tag for Accept-Language, read on every request so language switches apply at once. */
  getLocale?: () => string
  fetch?: typeof fetch
}

const MUTATING: ReadonlySet<Method> = new Set(['POST', 'PUT', 'PATCH', 'DELETE'])

export function isAbortError(error: unknown): boolean {
  // Duck-typed: DOMException is not an Error subclass in every realm (jsdom, iframes).
  return (
    typeof error === 'object' &&
    error !== null &&
    (error as { name?: unknown }).name === 'AbortError'
  )
}

function buildUrl(base: string, path: string, query?: Query): string {
  const url = `${base.replace(/\/$/, '')}/${path.replace(/^\//, '')}`
  if (!query) return url
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== null) params.set(key, String(value))
  }
  const text = params.toString()
  return text ? `${url}?${text}` : url
}

interface ErrorBody {
  error?: { code?: unknown; message?: unknown }
}

async function toApiError(response: Response): Promise<ApiError> {
  let code = `http_${response.status}`
  let message = response.statusText || code
  try {
    const body = (await response.json()) as ErrorBody
    if (typeof body.error?.code === 'string') code = body.error.code
    if (typeof body.error?.message === 'string') message = body.error.message
  } catch {
    // Not JSON: keep the status text.
  }
  return new ApiError(response.status, code, message)
}

export function createApiClient(options: ApiClientOptions = {}) {
  const baseUrl = options.baseUrl ?? '/api/v1'
  const getLocale = options.getLocale ?? (() => 'en')
  const doFetch = options.fetch ?? ((input, init) => globalThis.fetch(input, init))

  async function request<T>(method: Method, path: string, opts: RequestOptions = {}): Promise<T> {
    const headers: Record<string, string> = {
      Accept: 'application/json',
      'Accept-Language': getLocale(),
      ...opts.headers,
    }
    if (MUTATING.has(method)) headers['X-Seelegent'] = '1'
    let body: string | undefined
    if (opts.body !== undefined) {
      headers['Content-Type'] = 'application/json'
      body = JSON.stringify(opts.body)
    }

    let response: Response
    try {
      response = await doFetch(buildUrl(baseUrl, path, opts.query), {
        method,
        headers,
        body,
        signal: opts.signal,
        credentials: 'same-origin',
      })
    } catch (error) {
      if (isAbortError(error)) throw error
      throw new NetworkError(error instanceof Error ? error.message : String(error), error)
    }

    if (!response.ok) throw await toApiError(response)
    if (response.status === 204) return undefined as T
    const type = response.headers.get('Content-Type') ?? ''
    if (!type.includes('json')) return (await response.text()) as T
    try {
      return (await response.json()) as T
    } catch (error) {
      throw new ApiError(response.status, 'bad_response', String(error))
    }
  }

  return {
    request,
    get: <T>(path: string, opts?: Omit<RequestOptions, 'body'>) => request<T>('GET', path, opts),
    post: <T>(path: string, opts?: RequestOptions) => request<T>('POST', path, opts),
    put: <T>(path: string, opts?: RequestOptions) => request<T>('PUT', path, opts),
    patch: <T>(path: string, opts?: RequestOptions) => request<T>('PATCH', path, opts),
    delete: <T>(path: string, opts?: RequestOptions) => request<T>('DELETE', path, opts),
  }
}

export type ApiClient = ReturnType<typeof createApiClient>
