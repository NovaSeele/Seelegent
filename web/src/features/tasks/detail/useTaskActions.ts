/**
 * The writes a person can make on one task. Each returns whether it worked, applies the returned summary at once
 * (SSE confirms it a moment later) and reports exactly one toast: success where it helps, the daemon's message
 * on failure. `busy` names the action in flight so its button can show a spinner.
 */
import { shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'

import { ApiError, tasksApi, type TasksApi } from '@/api'
import type { FollowupRequest, LaneConflict, TaskSummary, Verdict } from '@/api/types'
import { useTasksStore } from '@/stores/tasks'
import { DURATION_LONG, useToastStore, type ToastOptions } from '@/stores/toast'

export type TaskActionName =
  'followup' | 'answer' | 'cancel' | 'takeover' | 'review' | 'dismiss' | 'copy'

export function useTaskActions(taskId: () => string, api: TasksApi = tasksApi) {
  const { t } = useI18n()
  const tasks = useTasksStore()
  const toast = useToastStore()
  const busy = shallowRef<TaskActionName | null>(null)

  /** The toast text of a failed write; a lane conflict names the files, the tasks that hold them and what to do. */
  function failure(error: unknown): string {
    if (error instanceof ApiError && error.details.reason === 'lane_conflict') {
      const conflicts = (error.details.conflicts ?? []) as readonly LaneConflict[]
      const tasks = conflicts.map((c) => c.title || c.taskId).join(', ')
      const files = [...new Set(conflicts.flatMap((c) => c.paths.map((p) => p.mine)))].join(', ')
      return t('task.actions.laneConflict', { tasks, files })
    }
    const message = error instanceof Error ? error.message : String(error)
    return t('task.actions.failed', { error: message })
  }

  /**
   * The toast of a review: accepting over a senior member's objection keeps the daemon's warnings in view (an info
   * toast that stays long enough to read), not a plain "Accepted".
   */
  function reviewToast(verdict: Verdict, sum: TaskSummary): ToastOptions {
    if (verdict !== 'accepted') return { message: t('task.review.rejectedToast'), tone: 'success' }
    const warnings = sum.warnings ?? []
    if (!warnings.length) return { message: t('task.review.acceptedToast'), tone: 'success' }
    return {
      message: t('task.review.acceptedWarned', { warnings: warnings.join('; ') }),
      tone: 'info',
      duration: DURATION_LONG,
    }
  }

  async function run(
    name: TaskActionName,
    call: () => Promise<TaskSummary>,
    success?: string | ((sum: TaskSummary) => ToastOptions),
  ): Promise<boolean> {
    if (busy.value) return false
    busy.value = name
    try {
      const sum = await call()
      tasks.upsertIfNewer(sum)
      if (typeof success === 'string') toast.show({ message: success, tone: 'success' })
      else if (success) toast.show(success(sum))
      return true
    } catch (error) {
      toast.show({ message: failure(error), tone: 'error' })
      return false
    } finally {
      busy.value = null
    }
  }

  return {
    busy,
    followup: (body: FollowupRequest) =>
      run('followup', () => api.followup(taskId(), body), t('task.followup.sent')),
    answer: (text: string) =>
      run('answer', () => api.answer(taskId(), text), t('task.question.sent')),
    cancel: () => run('cancel', () => api.cancel(taskId()), t('task.actions.cancelled')),
    takeover: () => run('takeover', () => api.takeover(taskId()), t('task.actions.tookOver')),
    review: (verdict: Verdict, notes?: string, answerObjection?: string) =>
      run(
        'review',
        () => api.review(taskId(), verdict, notes, answerObjection),
        (sum) => reviewToast(verdict, sum),
      ),
    dismiss: () => run('dismiss', () => api.dismissAttention(taskId())),
    async copyId(): Promise<boolean> {
      try {
        await navigator.clipboard.writeText(taskId())
        toast.show({ message: t('task.actions.copied'), tone: 'success' })
        return true
      } catch {
        toast.show({ message: t('task.actions.copyFailed'), tone: 'error' })
        return false
      }
    },
  }
}

export type TaskActions = ReturnType<typeof useTaskActions>
