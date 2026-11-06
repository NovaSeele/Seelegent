/**
 * The app's API client and event types. Payload types will come from types.gen.ts (tygo, milestone 5);
 * until then the events the old dashboard used are declared with `unknown` payloads so consumers must
 * validate them.
 */
import { i18n } from '@/i18n'

import { createApiClient } from './client'

export * from './client'

export const api = createApiClient({ getLocale: () => i18n.global.locale.value })

/** GET /api/v1/health */
export interface Health {
  status: string
  version?: string
}

/** Named SSE events on /api/v1/events. */
export interface ServerEvents {
  /** First event of every connection. */
  hello: { pid?: number; build?: string; version?: string }
  task: unknown
  event: unknown
  accounts: unknown
  roles: unknown
  members: unknown
  providers: unknown
  meeting: unknown
  contest: unknown
  ticket: unknown
  issues: unknown
  notes: unknown
  automation: unknown
  proof: unknown
}

export const EVENTS_URL = '/api/v1/events'
