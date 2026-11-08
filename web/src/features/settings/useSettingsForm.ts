/**
 * Autosave of the daemon's settings: each change is its own merge patch (PATCH /settings), sent when the control
 * commits (switches and lists at once, number fields after 500 ms without typing or on blur). A field that saved
 * shows a short "Saved" mark; a refused one keeps what was typed and shows the daemon's message for that field.
 * Settings shown come from the connection store, so a settings.updated frame from elsewhere updates every control
 * that is not being typed in (the fields keep their own text while focused).
 *
 * Settings checked against each other (LINKED_FIELDS) share their error: the daemon names one field, the message
 * shows on every field that can fix it, and it goes from all of them once one is saved or typed back.
 */
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage, fieldProblems } from '@/api/client'
import type { SettingsPatch } from '@/api/types'
import { useConnectionStore } from '@/stores/connection'
import { useSettingsStore } from '@/stores/settings'

import { LINKED_FIELDS, patchAt, valueAt } from './fields'

export const SAVED_MARK_MS = 2000

export function useSettingsForm() {
  const { t } = useI18n()
  const connection = useConnectionStore()
  const store = useSettingsStore()
  const errors = ref<Record<string, string>>({})
  const saved = ref<ReadonlySet<string>>(new Set())
  const saving = ref<ReadonlySet<string>>(new Set())
  const timers = new Map<string, ReturnType<typeof setTimeout>>()

  const settings = computed(() => connection.settings)

  function mark(set: ReadonlySet<string>, path: string, on: boolean): ReadonlySet<string> {
    const next = new Set(set)
    if (on) next.add(path)
    else next.delete(path)
    return next
  }

  /** `errors` without the error of `path` and the same error on the fields linked with it. */
  function without(path: string): Record<string, string> {
    const rest = { ...errors.value }
    const message = rest[path]
    delete rest[path]
    if (message !== undefined) {
      for (const other of LINKED_FIELDS[path] ?? []) if (rest[other] === message) delete rest[other]
    }
    return rest
  }

  /** Saves one value (null = back to the default). Returns whether it was accepted. */
  async function set(path: string, value: unknown): Promise<boolean> {
    if (!settings.value) return false
    saving.value = mark(saving.value, path, true)
    try {
      await store.save(patchAt(path, value) as SettingsPatch)
      errors.value = without(path)
      saved.value = mark(saved.value, path, true)
      clearTimeout(timers.get(path))
      timers.set(
        path,
        setTimeout(() => (saved.value = mark(saved.value, path, false)), SAVED_MARK_MS),
      )
      return true
    } catch (error) {
      const problems = fieldProblems(error)
      const next = { ...errors.value }
      for (const problem of problems) {
        next[problem.field] = problem.message
        // A rule between two settings names the other one in its message ("must be below minFreeMemoryMB").
        for (const other of LINKED_FIELDS[problem.field] ?? []) {
          if (problem.message.includes(other)) next[other] = problem.message
        }
      }
      if (!problems.some((p) => p.field === path)) {
        next[path] =
          problems[0]?.message ?? t('settings.daemon.failed', { error: errorMessage(error) })
      }
      errors.value = next
      return false
    } finally {
      saving.value = mark(saving.value, path, false)
    }
  }

  /** Shows `message` under the field without sending anything (a value the dashboard already knows is out of range). */
  function fail(path: string, message: string): void {
    errors.value = { ...errors.value, [path]: message }
  }

  /** The field shows the saved value again: its error, red border and aria-invalid go. */
  function clear(path: string): void {
    if (!(path in errors.value)) return
    errors.value = without(path)
  }

  return {
    settings,
    errors,
    saved,
    saving,
    get: (path: string) => valueAt(settings.value, path),
    set,
    fail,
    clear,
    reset: (path: string) => set(path, null),
  }
}

export type SettingsForm = ReturnType<typeof useSettingsForm>
