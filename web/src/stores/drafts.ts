/**
 * Unsent text per task (follow-up message, commit message), kept for the session so switching tasks or a live
 * update never loses what you typed. Not persisted: a draft is short-lived by nature.
 */
import { defineStore } from 'pinia'
import { shallowReactive } from 'vue'

export type DraftKind = 'followup' | 'commit'

const MAX_DRAFTS = 200

export const useDraftsStore = defineStore('drafts', () => {
  const drafts = shallowReactive(new Map<string, string>())

  const key = (kind: DraftKind, taskId: string) => `${kind}:${taskId}`

  function get(kind: DraftKind, taskId: string): string {
    return drafts.get(key(kind, taskId)) ?? ''
  }

  function set(kind: DraftKind, taskId: string, text: string): void {
    const k = key(kind, taskId)
    drafts.delete(k)
    if (!text) return
    drafts.set(k, text)
    // Oldest first in a Map: drop the oldest beyond the cap.
    while (drafts.size > MAX_DRAFTS) {
      const oldest = drafts.keys().next().value
      if (oldest === undefined) break
      drafts.delete(oldest)
    }
  }

  return { get, set }
})
