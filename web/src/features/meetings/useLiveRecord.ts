/**
 * One meeting, contest or ticket, read when its page opens and again on each update frame for its id and after a resync.
 * An action's answer (the record after the change) is applied at once with `set`.
 */
import { onUnmounted, shallowRef, watch } from 'vue'

import { useConnectionStore } from '@/stores/connection'
import { createLoader } from '@/stores/loader'

export function useLiveRecord<T extends { id: string }>(
  id: () => string,
  read: (id: string, signal: AbortSignal) => Promise<T>,
  event: 'meeting.updated' | 'contest.updated' | 'ticket.updated',
) {
  const record = shallowRef<T | null>(null)
  const loader = createLoader(
    (signal) => read(id(), signal),
    (data) => (record.value = data),
  )
  const connection = useConnectionStore()
  const stops = [
    connection.subscribe(event, (summary) => {
      if (summary.id === id()) void loader.load()
    }),
    connection.onResync(() => void loader.load()),
  ]
  onUnmounted(() => stops.forEach((stop) => stop()))
  watch(
    id,
    () => {
      record.value = null
      void loader.load()
    },
    { immediate: true },
  )

  function set(next: T): void {
    if (next.id === id()) record.value = next
  }

  return { record, status: loader.status, error: loader.error, reload: loader.load, set }
}
