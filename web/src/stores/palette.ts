/** Open state of the command palette (opened from the rail, the top bar and Mod+K). */
import { defineStore } from 'pinia'
import { ref } from 'vue'

export const usePaletteStore = defineStore('palette', () => {
  const isOpen = ref(false)
  /** Text the palette starts with (e.g. "theme " to jump straight to themes). */
  const initialQuery = ref('')

  function open(query = '') {
    initialQuery.value = query
    isOpen.value = true
  }

  function close() {
    isOpen.value = false
  }

  function toggle() {
    if (isOpen.value) close()
    else open()
  }

  return { isOpen, initialQuery, open, close, toggle }
})
