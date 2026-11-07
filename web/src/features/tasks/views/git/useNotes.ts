/**
 * The review notes of one task: read when the Git view opens, again on notes.changed for the task and after a
 * resync. Each write answers the note (or the sent ids) and the list is read again, since a send changes every
 * draft's status.
 */
import { computed, onUnmounted, shallowRef, watch } from 'vue'

import type { Note, NoteInput } from '@/api/types'
import { useConnectionStore } from '@/stores/connection'
import { createLoader } from '@/stores/loader'
import { useTeamStore } from '@/stores/team'

/** Notes that the next send includes: drafts and those the last turn left unaddressed. */
export const sendable = (n: Note) => n.status === 'draft' || n.status === 'unaddressed'

export function useNotes(taskId: () => string) {
  const team = useTeamStore()
  const notes = shallowRef<readonly Note[]>([])
  const loader = createLoader(
    (signal) => team.api().notes(taskId(), signal),
    (list) => (notes.value = list),
  )
  const connection = useConnectionStore()
  const stops = [
    connection.subscribe('notes.changed', ({ taskId: id }) => {
      if (id === taskId()) void loader.load()
    }),
    connection.onResync(() => void loader.load()),
  ]
  onUnmounted(() => stops.forEach((stop) => stop()))
  watch(
    taskId,
    () => {
      notes.value = []
      void loader.load()
    },
    { immediate: true },
  )

  const toSend = computed(() => notes.value.filter(sendable))

  async function add(input: NoteInput): Promise<void> {
    const n = await team.api().addNote(taskId(), { ...input, by: 'user' })
    notes.value = [...notes.value, n]
    void loader.load()
  }

  async function setState(id: string, state: 'resolved' | 'open'): Promise<void> {
    const n = await team.api().patchNote(taskId(), id, { state })
    notes.value = notes.value.map((x) => (x.id === id ? n : x))
  }

  async function remove(id: string): Promise<void> {
    await team.api().deleteNote(taskId(), id)
    notes.value = notes.value.filter((x) => x.id !== id)
  }

  async function send(extra: string): Promise<number> {
    const res = await team
      .api()
      .sendNotes(taskId(), { by: 'user', ...(extra.trim() ? { extra: extra.trim() } : {}) })
    void loader.load()
    return res.sent.length
  }

  return {
    notes,
    toSend,
    status: loader.status,
    error: loader.error,
    reload: loader.load,
    add,
    setState,
    remove,
    send,
  }
}

export type NotesModel = ReturnType<typeof useNotes>
