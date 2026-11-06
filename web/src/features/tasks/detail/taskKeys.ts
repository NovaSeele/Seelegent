/**
 * The open task's views and default shortcuts (palette commands, button tooltips and the help all read these).
 * Single keys fire only outside text fields and never while a dialog or the palette is open.
 */
import type { IconName } from '@/components/ui/icons'
import { isOneOf, type StoredValue } from '@/lib/storage'

export const TASK_VIEWS = ['terminal', 'tree', 'graph', 'files', 'git'] as const
export type TaskView = (typeof TASK_VIEWS)[number]

export const VIEW_ICONS: Readonly<Record<TaskView, IconName>> = {
  terminal: 'terminal',
  tree: 'tree',
  graph: 'graph',
  files: 'file',
  git: 'git',
}

/** Views 1-5 in tab order. */
export const viewShortcut = (view: TaskView) => String(TASK_VIEWS.indexOf(view) + 1)

export const TASK_VIEW: StoredValue<TaskView> = {
  name: 'task.view',
  fallback: 'terminal',
  validate: isOneOf(TASK_VIEWS),
}

export const TASK_KEYS = {
  followup: 'f',
  accept: 'a',
  reject: 'r',
  cancel: 'x',
  takeover: 't',
  nextNeed: 'b',
  // "g o" is Automations, so finding a task takes the usual search key.
  find: '/',
} as const
