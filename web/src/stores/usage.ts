/**
 * Token usage (GET /usage/summary) and quota history (GET /usage/history) for the chosen range, 7 or 30 days
 * (saved per viewer). Seven days read the history by hour, thirty by day. Read again when the range changes, on
 * the refresh button and after a resync; usage has no change frame.
 */
import { defineStore } from 'pinia'
import { computed, shallowRef, watch } from 'vue'

import { usageApi as defaultApi } from '@/api'
import type { UsageApi } from '@/api/usage'
import type { History, Summary } from '@/api/types'
import { readStored, writeStored, type StoredValue } from '@/lib/storage'

import { useConnectionStore } from './connection'
import { createLoader } from './loader'

export type UsageRange = 7 | 30

export const USAGE_RANGE: StoredValue<UsageRange> = {
  name: 'usage.range',
  fallback: 7,
  validate: (value): value is UsageRange => value === 7 || value === 30,
}

const DAY_MS = 86_400_000

export const useUsageStore = defineStore('usage', () => {
  let client: UsageApi = defaultApi
  let now = () => Date.now()
  const days = shallowRef<UsageRange>(readStored(USAGE_RANGE))
  const summary = shallowRef<Summary | null>(null)
  const history = shallowRef<History | null>(null)

  const summaryLoader = createLoader(
    (signal) => client.summary(days.value, signal),
    (data) => (summary.value = data),
  )
  const historyLoader = createLoader(
    (signal) =>
      client.history(
        {
          bucket: days.value === 7 ? 'hour' : 'day',
          since: new Date(now() - days.value * DAY_MS).toISOString(),
        },
        signal,
      ),
    (data) => (history.value = data),
  )
  let watching = false

  function setApi(next: UsageApi, clock?: () => number): void {
    client = next
    if (clock) now = clock
  }

  function load(): Promise<void> {
    return Promise.all([summaryLoader.load(), historyLoader.load()]).then(() => undefined)
  }

  function ensure(): void {
    if (watching) return
    watching = true
    useConnectionStore().onResync(() => void load())
    void load()
  }

  watch(days, (value) => {
    writeStored(USAGE_RANGE, value)
    if (watching) void load()
  })

  return {
    days,
    summary,
    history,
    summaryStatus: summaryLoader.status,
    summaryError: summaryLoader.error,
    historyStatus: historyLoader.status,
    historyError: historyLoader.error,
    busy: computed(() => summaryLoader.busy.value || historyLoader.busy.value),
    setApi,
    ensure,
    load,
  }
})
