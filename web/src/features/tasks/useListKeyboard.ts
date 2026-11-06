/**
 * Keyboard for the Tasks sidebar list: one row is in the tab order (roving); arrows walk the rows like a tree
 * (listKey), Left/Right fold groups, Space toggles a row in select mode, Ctrl+A selects every row and Esc leaves
 * select mode.
 */
import { computed, nextTick, ref, type Ref } from 'vue'

import { listKey, rowKey, type ListRow } from './listModel'
import type { TaskSelection } from './useTaskSelection'

export function useListKeyboard(
  rows: Readonly<Ref<readonly ListRow[]>>,
  list: Readonly<Ref<HTMLElement | null>>,
  selection: Pick<TaskSelection, 'selectMode' | 'onToggle' | 'selectEvery'>,
  toggleGroup: (key: string, force?: boolean) => void,
) {
  const focusKey = ref<string | null>(null)
  const tabStop = computed(() => {
    const keys = rows.value.map(rowKey)
    if (focusKey.value && keys.includes(focusKey.value)) return focusKey.value
    return keys[0] ?? null
  })

  function focusRow(key: string) {
    focusKey.value = key
    void nextTick(() =>
      list.value?.querySelector<HTMLElement>(`[data-key="${CSS.escape(key)}"]`)?.focus(),
    )
  }

  function onKeydown(event: KeyboardEvent) {
    if (event.isComposing || event.altKey) return
    const { selectMode } = selection
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'a') {
      event.preventDefault()
      selection.selectEvery()
      return
    }
    if (event.key === 'Escape' && selectMode.value) {
      event.preventDefault()
      event.stopPropagation()
      selectMode.value = false
      return
    }
    const current = focusKey.value
    if (event.key === ' ' && selectMode.value && current?.startsWith('t:')) {
      event.preventDefault()
      selection.onToggle(current.slice(2), event.shiftKey)
      return
    }
    if (event.ctrlKey || event.metaKey) return
    const action = listKey(rows.value, current, event.key)
    if (!action) return
    event.preventDefault()
    if ('focus' in action) focusRow(action.focus)
    else if ('collapse' in action) toggleGroup(action.collapse, true)
    else toggleGroup(action.expand, false)
  }

  return { focusKey, tabStop, onKeydown }
}
