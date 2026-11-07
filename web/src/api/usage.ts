/** Typed endpoints of usage (contract §4): the token summary and the hourly / daily account history. */
import type { ApiClient } from './client'
import type { History, Summary } from './types'

export interface HistoryQuery {
  /** RFC 3339; the daemon defaults to now−7d and clamps to now−30d. */
  since?: string
  accountId?: string
  bucket?: History['bucket']
}

export function createUsageApi(client: ApiClient) {
  return {
    /** days 1..30 (7). */
    summary: (days: number, signal?: AbortSignal) =>
      client.get<Summary>('/usage/summary', { signal, query: { days } }),
    history: (query: HistoryQuery = {}, signal?: AbortSignal) =>
      client.get<History>('/usage/history', {
        signal,
        query: { since: query.since, accountId: query.accountId, bucket: query.bucket },
      }),
  }
}

export type UsageApi = ReturnType<typeof createUsageApi>
