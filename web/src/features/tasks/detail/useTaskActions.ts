/**
 * The writes a person can make on one task. Each returns whether it worked, applies the returned summary at once
 * (SSE confirms it a moment later) and reports exactly one toast: success where it helps, the daemon's message
 * on failure. `busy` names the action in flight so its button can show a spinner.
 */
import { shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'

import { tasksApi, type TasksApi } from '@/api'
import type { FollowupRequest, TaskSummary, Verdict } from '@/api/types'
import { useTasksStore } from '@/stores/tasks'
import { useToastStore } from '@/stores/toast'

export type TaskActionName =
  'followup' | 'answer' | 'cancel' | 'takeover' | 'review' | 'dismiss' | 'copy'

export function useTaskActions(taskId: () => string, api: TasksApi = tasksApi) {
  const { t } = useI18n()
  const tasks = useTasksStore()
  const toast = useToastStore()
  const busy = shallowRef<TaskActionName | null>(null)

  async function run(
    name: TaskActionName,
    call: () => Promise<TaskSummary>,
    success?: string,
  ): Promise<boolean> {
    if (busy.value) return false
    busy.value = name
    try {
      tasks.upsertIfNewer(await call())
      if (success) toast.show({ message: success, tone: 'success' })
      return true
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error)
      toast.show({ message: t('task.actions.failed', { error: message }), tone: 'error' })
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
    review: (verdict: Verdict, notes?: string) =>
      run(
        'review',
        () => api.review(taskId(), verdict, notes),
        t(verdict === 'accepted' ? 'task.review.acceptedToast' : 'task.review.rejectedToast'),
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
