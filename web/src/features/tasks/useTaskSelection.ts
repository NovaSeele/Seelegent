/**
 * Selection in the Tasks sidebar: select mode, the selected ids (pruned as rows go away), what the selected tasks
 * allow in bulk (cancel, accept) and the bulk dialog's action. Registers the "Select tasks" command while mounted.
 */
import { computed, onBeforeUnmount, ref, shallowRef, watch, type Ref } from 'vue'

import { awaitsReview, canCancel } from '@/lib/taskStatus'
import { useCommandsStore } from '@/stores/commands'
import { useTasksStore } from '@/stores/tasks'

import type { BulkAction } from './BulkDialog.vue'
import { EMPTY_SELECTION, extendTo, prune, selectAll, toggle } from './selection'

export function useTaskSelection(order: Readonly<Ref<readonly string[]>>) {
  const tasks = useTasksStore()
  const selectMode = ref(false)
  const selection = shallowRef(EMPTY_SELECTION)
  watch(order, (value) => (selection.value = prune(selection.value, value)))
  watch(selectMode, (on) => !on && (selection.value = EMPTY_SELECTION))
  const selectedIds = computed(() => order.value.filter((id) => selection.value.ids.has(id)))

  /** Click or Space on a row; `extend` (Shift) selects the range from the anchor. */
  function onToggle(id: string, extend: boolean) {
    selectMode.value = true
    selection.value = extend
      ? extendTo(selection.value, order.value, id)
      : toggle(selection.value, id)
  }

  function selectEvery() {
    selectMode.value = true
    selection.value = selectAll(order.value)
  }

  const eligible = computed(() => {
    const pick = (test: typeof canCancel) =>
      selectedIds.value.filter((id) => {
        const task = tasks.get(id)
        return !!task && test(task)
      })
    return { cancel: pick(canCancel), accept: pick(awaitsReview) }
  })

  const bulk = ref<BulkAction | null>(null)
  function onBulkDone() {
    bulk.value = null
    selectMode.value = false
  }

  const unregister = useCommandsStore().register({
    id: 'tasks.select',
    title: { key: 'tasks.cmd.select' },
    group: 'tasks',
    icon: 'select',
    when: () => !selectMode.value,
    run: () => {
      selectMode.value = true
    },
  })
  onBeforeUnmount(unregister)

  return { selectMode, selection, selectedIds, eligible, bulk, onToggle, selectEvery, onBulkDone }
}

export type TaskSelection = ReturnType<typeof useTaskSelection>
