/**
 * An attention flag in the reader's language (F14): the title from its reason code, the explanation from its params.
 * The daemon's English detail is shown only behind "Seelegent said:" (`said`) when the flag has no params to build
 * a sentence from (a failure, an unknown reason, a flag older than the params); a worker's question and a
 * verifier's reason are the worker's own words and stay as they are (`quote`).
 */
import type { Attention, TaskSummary } from '@/api/types'
import { distinctMB, type FormatMB } from '@/lib/memory'

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

/**
 * A task title placed inside a sentence: without the full stop it ends with, so "The earlier task Migrate the cart.
 * (t1) failed." does not stop twice. A task's title is the first line of its prompt, often a sentence; an ellipsis
 * stays.
 */
export function inSentence(title: string): string {
  const trimmed = title.trim()
  if (/(\.\.\.|…)$/.test(trimmed)) return trimmed
  return trimmed.replace(/[.。．]+$/, '')
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

/** The free and needed memory of a wait, never reading the same when they differ. */
function amounts(mb: FormatMB, available: number, need: number) {
  const [a = '', n = ''] = distinctMB(mb, [available, need])
  return { available: a, need: n }
}

/** Memory amounts when the caller has no formatter (the daemon's MB as they are). */
const plainMB: FormatMB = (mb) => `${mb} MB`

/**
 * The line a task waiting for resources shows under its status (Watch card, task header): the resources_wait
 * flag's sentence, or "" for any other task. Other info flags stay out of sight: they are notes, not news.
 */
export function resourcesWaitLine(
  task: Pick<TaskSummary, 'attention'>,
  t: Translate,
  te: HasKey,
  mb: FormatMB = plainMB,
): string {
  const a = task.attention
  return a?.reason === 'resources_wait' ? attentionText(a, t, te, mb).why : ''
}

export function attentionText(
  a: Attention,
  t: Translate,
  te: HasKey,
  mb: FormatMB = plainMB,
): AttentionText {
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
    case 'failed': {
      // How the worker process ended, from its code (design review: "agy exited with code 0 and no result").
      const exitKey = `task.attention.why.exit.${p?.kind ?? ''}`
      if (p?.kind && te(exitKey)) {
        const ended = t(exitKey, { backend: p.backend ?? '', code: p.exitCode ?? '' })
        out.why = `${ended} ${why('failed')}`
        return out
      }
      out.why = why('failed')
      out.said = a.detail ?? ''
      return out
    }
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
        title: inSentence(p.title || p.task || ''),
      })
      break
    case 'hygiene':
      out.why = why('hygiene', { count: p.count ?? 0 }, p.count ?? 0)
      break
    case 'work_at_risk':
      out.why = why('workAtRisk', { count: p.count ?? 0 }, p.count ?? 0)
      break
    case 'resources_wait':
      // Free memory (or commit) below the threshold that holds the task; the lead cannot act on it.
      out.why =
        p.availableMB !== undefined && p.needMB
          ? why('resourcesWait', {
              minutes: p.minutes ?? 0,
              ...amounts(mb, p.availableMB, p.needMB),
            })
          : why('resourcesWaitPlain', { minutes: p.minutes ?? 0 })
      break
    default:
      out.said = a.detail ?? a.reason
  }
  return out
}
