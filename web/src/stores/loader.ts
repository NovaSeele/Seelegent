/**
 * A read that a page triggers more than once (first view, a change frame, a resync, the refresh button): only the
 * newest call's answer applies, an older one that arrives late is dropped, and a failed reload keeps the data on
 * screen with the error next to it.
 */
import { shallowRef } from 'vue'

import { errorMessage, isAbortError } from '@/api/client'

export type LoadStatus = 'idle' | 'loading' | 'ready' | 'error'

export function createLoader<T>(
  read: (signal: AbortSignal) => Promise<T>,
  apply: (data: T) => void,
) {
  const status = shallowRef<LoadStatus>('idle')
  const error = shallowRef<string | null>(null)
  /** A read is running (also a reload while older data stays on screen). */
  const busy = shallowRef(false)
  let controller: AbortController | null = null

  async function load(): Promise<void> {
    controller?.abort()
    const mine = new AbortController()
    controller = mine
    busy.value = true
    if (status.value !== 'ready') status.value = 'loading'
    try {
      const data = await read(mine.signal)
      if (controller !== mine) return
      apply(data)
      status.value = 'ready'
      error.value = null
    } catch (cause) {
      if (controller !== mine || isAbortError(cause)) return
      error.value = errorMessage(cause)
      if (status.value !== 'ready') status.value = 'error'
    } finally {
      if (controller === mine) {
        controller = null
        busy.value = false
      }
    }
  }

  return { status, error, busy, load }
}
