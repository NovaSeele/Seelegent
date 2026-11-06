/**
 * Palette entries and shortcuts of the open task (follow up, accept, reject, cancel, take over, copy id, views
 * 1-5), registered while its detail is mounted. Each applies only when the task allows it (`when`).
 */
import { onBeforeUnmount } from 'vue'

import type { TaskSummary } from '@/api/types'
import { awaitsReview, canCancel, canFollowup, canTakeOver } from '@/lib/taskStatus'
import { useCommandsStore, type Command } from '@/stores/commands'

import { TASK_KEYS, TASK_VIEWS, VIEW_ICONS, viewShortcut, type TaskView } from './taskKeys'
import type { TaskDialog } from './TaskDialogs.vue'
import type { TaskActions } from './useTaskActions'

export interface TaskCommandHandlers {
  task: () => TaskSummary | undefined
  actions: TaskActions
  focusFollowup: () => void
  openDialog: (dialog: TaskDialog) => void
  setView: (view: TaskView) => void
}

export function useTaskCommands(h: TaskCommandHandlers) {
  const commands = useCommandsStore()
  const is = (test: (task: TaskSummary) => boolean) => () => {
    const task = h.task()
    return !!task && test(task)
  }

  const list: Command[] = [
    {
      id: 'task.followup',
      title: { key: 'task.cmd.followup' },
      group: 'task',
      icon: 'send',
      shortcut: TASK_KEYS.followup,
      when: is(canFollowup),
      run: () => h.focusFollowup(),
    },
    {
      id: 'task.accept',
      title: { key: 'task.cmd.accept' },
      group: 'task',
      icon: 'statusAccepted',
      shortcut: TASK_KEYS.accept,
      when: is(awaitsReview),
      run: () => h.openDialog({ kind: 'review', verdict: 'accepted' }),
    },
    {
      id: 'task.reject',
      title: { key: 'task.cmd.reject' },
      group: 'task',
      icon: 'statusRejected',
      shortcut: TASK_KEYS.reject,
      when: is(awaitsReview),
      run: () => h.openDialog({ kind: 'review', verdict: 'rejected' }),
    },
    {
      id: 'task.cancel',
      title: { key: 'task.cmd.cancel' },
      group: 'task',
      icon: 'statusCancelled',
      shortcut: TASK_KEYS.cancel,
      when: is(canCancel),
      run: () => h.openDialog({ kind: 'cancel' }),
    },
    {
      id: 'task.takeover',
      title: { key: 'task.cmd.takeover' },
      group: 'task',
      icon: 'hand',
      shortcut: TASK_KEYS.takeover,
      when: is(canTakeOver),
      run: () => h.openDialog({ kind: 'takeover' }),
    },
    {
      id: 'task.copyId',
      title: { key: 'task.cmd.copyId' },
      group: 'task',
      icon: 'copy',
      when: () => !!h.task(),
      run: () => void h.actions.copyId(),
    },
    ...TASK_VIEWS.map((view): Command => ({
      id: `task.view.${view}`,
      title: { key: 'task.cmd.view', params: { name: { key: `task.views.${view}` } } },
      group: 'task',
      icon: VIEW_ICONS[view],
      shortcut: viewShortcut(view),
      when: () => !!h.task(),
      run: () => h.setView(view),
    })),
  ]

  const unregister = commands.register(...list)
  onBeforeUnmount(unregister)
}
