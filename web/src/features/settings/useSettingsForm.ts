/**
 * Autosave of the daemon's settings: each change is its own merge patch (PATCH /settings), sent when the control
 * commits (switches and lists at once, number fields after 500 ms without typing or on blur). A field that saved
 * shows a short "Saved" mark; a refused one keeps what was typed and shows the daemon's message for that field.
 * Settings shown come from the connection store, so a settings.updated frame from elsewhere updates every control
 * that is not being typed in (the fields keep their own text while focused).
 */
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage, fieldProblems } from '@/api/client'
import type { SettingsPatch } from '@/api/types'
import { useConnectionStore } from '@/stores/connection'
import { useSettingsStore } from '@/stores/settings'

import { patchAt, valueAt } from './fields'

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

  /** Saves one value (null = back to the default). Returns whether it was accepted. */
  async function set(path: string, value: unknown): Promise<boolean> {
    if (!settings.value) return false
    saving.value = mark(saving.value, path, true)
    try {
      await store.save(patchAt(path, value) as SettingsPatch)
      const rest = { ...errors.value }
      delete rest[path]
      errors.value = rest
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
      for (const problem of problems) next[problem.field] = problem.message
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

  return {
    settings,
    errors,
    saved,
    saving,
    get: (path: string) => valueAt(settings.value, path),
    set,
    reset: (path: string) => set(path, null),
  }
}

export type SettingsForm = ReturnType<typeof useSettingsForm>
