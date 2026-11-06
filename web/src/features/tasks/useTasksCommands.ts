/**
 * App-wide task commands, registered by the shell: one palette entry per task while searching ("Task: title"),
 * "/" to find a task (opens the palette with that prefix), and "b" to jump to the next task that needs you.
 */
import { onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import type { TaskSummary } from '@/api/types'
import { buildBoard } from '@/features/watch/watchModel'
import { isVisibleTask } from '@/lib/taskStatus'
import { useCommandsStore, type Command } from '@/stores/commands'
import { usePaletteStore } from '@/stores/palette'
import { useTasksStore } from '@/stores/tasks'
import { useToastStore } from '@/stores/toast'

import { TASK_KEYS } from './detail/taskKeys'
import { fold } from './listModel'

/** At most this many task entries join one palette search. */
export const TASK_SOURCE_LIMIT = 30

/** Tasks whose title, id or project contain every word of the query (the "Task:" prefix ignored), newest first. */
export function findTasks(
  tasks: Iterable<TaskSummary>,
  query: string,
  prefix: string,
  limit = TASK_SOURCE_LIMIT,
): TaskSummary[] {
  let q = fold(query.trim())
  const p = fold(prefix.trim())
  if (p && q.startsWith(p)) q = q.slice(p.length).trim()
  const words = q.split(/\s+/).filter(Boolean)
  if (!words.length) return []
  const found: TaskSummary[] = []
  for (const task of tasks) {
    if (!isVisibleTask(task)) continue
    const text = fold(`${task.title} ${task.id} ${task.projectName}`)
    if (words.every((word) => text.includes(word))) found.push(task)
  }
  return found.sort((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt)).slice(0, limit)
}

/** The need after `current` in Watch order (wrapping), or the first one. */
export function nextNeed(
  needs: readonly string[],
  current: string | undefined,
): string | undefined {
  const index = current ? needs.indexOf(current) : -1
  return needs[(index + 1) % needs.length] ?? needs[0]
}

export function useTasksCommands() {
  const { t } = useI18n()
  const router = useRouter()
  const commands = useCommandsStore()
  const palette = usePaletteStore()
  const tasks = useTasksStore()
  const toast = useToastStore()

  const prefix = () => t('tasks.cmd.open', { title: '' }).trim()
  const open = (id: string) => router.push({ name: 'tasks', params: { taskId: id } })
  const currentId = () => {
    const param = router.currentRoute.value.params.taskId
    return typeof param === 'string' ? param : undefined
  }

  const unregister = commands.register(
    {
      id: 'tasks.find',
      title: { key: 'tasks.cmd.search' },
      group: 'tasks',
      icon: 'search',
      shortcut: TASK_KEYS.find,
      run: () => palette.open(`${prefix()} `),
    },
    {
      id: 'tasks.nextNeed',
      title: { key: 'tasks.cmd.nextNeed' },
      group: 'tasks',
      icon: 'arrowDown',
      shortcut: TASK_KEYS.nextNeed,
      run: () => {
        const needs = buildBoard(tasks.summaries.values(), Date.now()).needs.map((n) => n.id)
        const id = nextNeed(needs, currentId())
        if (id) void open(id)
        else toast.show({ message: t('tasks.cmd.noNeeds') })
      },
    },
  )
  const removeSource = commands.addSource((query) =>
    findTasks(tasks.summaries.values(), query, prefix()).map((task): Command => ({
      id: `tasks.open.${task.id}`,
      title: { key: 'tasks.cmd.open', params: { title: task.title } },
      group: 'tasks',
      icon: 'tasks',
      run: () => void open(task.id),
    })),
  )
  onBeforeUnmount(() => {
    unregister()
    removeSource()
  })
}
