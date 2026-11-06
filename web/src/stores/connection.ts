import { defineStore } from 'pinia'
import { ref } from 'vue'

export type ConnectionStatus = 'connecting' | 'online' | 'offline'

/** Connection state of the dashboard to the daemon; the SSE client will drive it. */
export const useConnectionStore = defineStore('connection', () => {
  const status = ref<ConnectionStatus>('connecting')

  function setStatus(next: ConnectionStatus): void {
    status.value = next
  }

  return { status, setStatus }
})
