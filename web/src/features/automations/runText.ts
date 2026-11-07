/**
 * A run of an automation in the viewer's language: the daemon records the facts (AutomationRun.info) and this
 * builds the sentence, with locale-formatted durations and percentages. A command's own output stays as it is,
 * under a label. Rows older than the facts keep the daemon's English detail.
 */
import type { AutomationRun } from '@/api/types'
import type { Formatters } from '@/composables/useFormat'

export type Translate = (key: string, params?: Record<string, unknown>) => string

export interface RunView {
  /** Why the run ended as it did, in a sentence ("" when there is nothing to say). */
  why: string
  /** A command's output or the daemon's message, verbatim, and the label that goes before it. */
  output: string
  outputLabel: string
}

type Fmt = Pick<Formatters, 'duration' | 'percent' | 'dateTime'>

const MINUTE = 60_000

/** A pending task's status (the daemon's name) to its word in common.status. */
const STATUS_WORD: Readonly<Record<string, string>> = {
  queued: 'queued',
  running: 'running',
  waiting_quota: 'waiting',
  waiting_deps: 'waitingDeps',
}

export function runView(run: AutomationRun, t: Translate, fmt: Fmt): RunView {
  const info = run.info
  const view: RunView = { why: '', output: '', outputLabel: '' }
  if (!info) {
    view.why = run.detail ?? ''
    return view
  }
  const pct = (n: number | undefined) => fmt.percent((n ?? 0) / 100)
  switch (run.outcome) {
    case 'skipped_overlap':
      view.why =
        info.reason === 'task_running'
          ? t('automations.run.taskRunning', {
              status: t(`common.status.${STATUS_WORD[info.taskStatus ?? ''] ?? 'running'}.label`),
            })
          : t('automations.run.starting')
      break
    case 'skipped_quota':
      view.why = info.account
        ? t('automations.run.quotaBucket', {
            account: info.account,
            left: pct(info.remaining),
            min: pct(info.minRemaining),
          })
        : t('automations.run.quotaBackend', {
            backend: info.backend ?? '',
            left: pct(info.remaining),
            min: pct(info.minRemaining),
          })
      break
    case 'skipped_precheck':
    case 'precheck_failed':
      if (info.timedOut) {
        view.why = t('automations.run.timedOut', {
          limit: fmt.duration((info.timeoutSeconds ?? 60) * 1000),
        })
      } else if (info.exitCode !== undefined) {
        view.why = t(
          info.exitCode === 1 ? 'automations.run.saidSkip' : 'automations.run.exitCode',
          { code: info.exitCode },
        )
      } else {
        view.why = t('automations.run.notStarted')
      }
      if (info.output) {
        view.output = info.output
        view.outputLabel = t('automations.run.output')
      }
      break
    case 'skipped_late':
      view.why = t('automations.run.late', {
        late: fmt.duration((info.lateMinutes ?? 0) * MINUTE),
        grace: fmt.duration((info.graceMinutes ?? 0) * MINUTE),
      })
      break
    case 'skipped_changed':
      view.why = t(`automations.run.changed.${info.reason ?? 'changed'}`)
      break
    case 'error':
      if (info.reason === 'member_missing' || info.reason === 'member_disabled') {
        const key = info.reason === 'member_missing' ? 'memberMissing' : 'memberDisabled'
        view.why = t(`automations.run.${key}`, { member: info.member ?? '' })
      } else if (info.reason === 'interrupted') {
        view.why = t('automations.run.interrupted')
      } else {
        view.why = t('automations.run.couldNotStart')
        if (info.message) {
          view.output = info.message
          view.outputLabel = t('automations.run.daemonSaid')
        }
      }
      break
    case 'pending':
      if (info.due) view.why = t('automations.run.due', { when: fmt.dateTime(info.due) })
      break
    default:
      view.why = run.detail ?? ''
  }
  return view
}
