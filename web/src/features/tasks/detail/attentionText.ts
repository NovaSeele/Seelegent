/**
 * An attention flag in the reader's language (F14): the title from its reason code, the explanation from its params.
 * The daemon's English detail is shown only behind "Seelegent said:" (`said`) when the flag has no params to build
 * a sentence from (a failure, an unknown reason, a flag older than the params); a worker's question and a
 * verifier's reason are the worker's own words and stay as they are (`quote`).
 */
import type { Attention, TaskSummary } from '@/api/types'

import { reasonKey } from './question'

export type Translate = (key: string, named?: Record<string, unknown>, plural?: number) => string
export type HasKey = (key: string) => boolean

export interface AttentionText {
  title: string
  /** Why, in a sentence ("" when the title says it all). */
  why: string
  /** Someone's own words to show as they are (a verifier's reason). */
  quote: string
  /** The daemon's English message for the disclosure ("" when the sentence says it). */
  said: string
}

/** The params' kind of a deps_blocked flag to its key under task.attention.why. */
const DEP_KEY: Readonly<Record<string, string>> = {
  missing: 'depMissing',
  rejected: 'depRejected',
  failed: 'depFailed',
  cancelled: 'depCancelled',
}

export function attentionTitle(a: Pick<Attention, 'reason'>, t: Translate, te: HasKey): string {
  const key = `common.attention.${reasonKey(a.reason)}`
  return te(key) ? t(key) : t('common.attention.unknown')
}

/**
 * A line on a task's dependencies in the reader's language, never the daemon's English `depsReason`: why it is
 * blocked (from its deps_blocked flag), or which tasks it waits for ("" otherwise).
 */
export function depsText(
  task: Pick<TaskSummary, 'status' | 'after' | 'attention'>,
  t: Translate,
  te: HasKey,
): string {
  if (task.status === 'blocked_deps' && task.attention?.reason === 'deps_blocked') {
    return attentionText(task.attention, t, te).why
  }
  if (task.status === 'waiting_deps' && task.after.length) {
    return t('task.attention.why.depsWaiting', { tasks: task.after.join(', ') })
  }
  return ''
}

export function attentionText(a: Attention, t: Translate, te: HasKey): AttentionText {
  const out: AttentionText = { title: attentionTitle(a, t, te), why: '', quote: '', said: '' }
  const p = a.params
  const why = (key: string, named?: Record<string, unknown>, plural?: number) =>
    plural === undefined
      ? t(`task.attention.why.${key}`, named ?? {})
      : t(`task.attention.why.${key}`, named ?? {}, plural)
  switch (a.reason) {
    case 'question':
    case 'review':
    case 'gate_red':
    case 'peer_objection':
      // The banner shows these from the task itself (question card, gate steps, objections).
      return out
    case 'background_wait':
      out.why = why('backgroundWait')
      return out
    case 'verifier_rejected':
      out.quote = a.detail ?? ''
      return out
    case 'failed':
      out.why = why('failed')
      out.said = a.detail ?? ''
      return out
    case 'no_response':
      out.why = p?.denied?.length
        ? why('denied', { actions: p.denied.join(', ') })
        : why('noResponse')
      return out
  }
  if (!p) {
    out.said = a.detail ?? ''
    if (!te(`common.attention.${reasonKey(a.reason)}`)) out.said ||= a.reason
    return out
  }
  switch (a.reason) {
    case 'stalled':
      if (p.kind === 'not_started') out.why = why('notStarted', { minutes: p.minutes ?? 0 })
      else if (p.kind === 'tool')
        out.why = why('toolQuiet', {
          minutes: p.minutes ?? 0,
          step: p.command ? `${p.tool ?? ''} "${p.command}"` : (p.tool ?? ''),
        })
      else out.why = why('noOutput', { minutes: p.minutes ?? 0 })
      break
    case 'looping':
      out.why =
        p.kind === 'repeated'
          ? why('repeated', { tool: p.tool ?? '', count: p.count ?? 0, steps: p.steps ?? 0 })
          : why('callsFailed', { count: p.count ?? 0 })
      break
    case 'no_accounts':
      out.why = why('noAccounts', { backend: p.backend ?? '' })
      break
    case 'auth':
      out.why = why('auth', { backend: p.backend ?? '' })
      break
    case 'deps_blocked':
      out.why = why(DEP_KEY[p.kind ?? ''] ?? 'depFailed', {
        task: p.task ?? '',
        title: p.title || p.task,
      })
      break
    case 'hygiene':
      out.why = why('hygiene', { count: p.count ?? 0 }, p.count ?? 0)
      break
    case 'work_at_risk':
      out.why = why('workAtRisk', { count: p.count ?? 0 }, p.count ?? 0)
      break
    default:
      out.said = a.detail ?? a.reason
  }
  return out
}
