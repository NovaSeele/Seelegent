/**
 * The Terminal transcript as plain data: task events folded into display items. Streamed text pieces of one step
 * become one text item, a tool call and its result become one tool item (with the file it touched), and the
 * remaining event types become short notices. Each item has a stable key (its first event's seq) and a revision
 * (the last seq that changed it); `buildTimeline` hands back the previous object for an unchanged item, so a new
 * event re-renders only the item it touches.
 */
import type { TaskEvent } from '@/api/events'
import type {
  LeadMessageData,
  RouteData,
  TurnEndData,
  Usage,
  WorkerReport,
  WorkerTool,
} from '@/api/types'
import type { IconName } from '@/components/ui/icons'

import { reasonKey } from '../../detail/question'
import { gateNotice, laneNotice, verifyNotice } from './gateNotices'

interface Base {
  key: string
  rev: number
  turn?: number
  attempt?: number
  ts: string
}

export type Tone = 'info' | 'ok' | 'warn' | 'error' | 'dim'

export interface Message {
  key: string
  params?: Record<string, string | number>
  /** Shown instead when the catalog has no `key` (an attention reason this dashboard does not know). */
  fallback?: string
}

/** A line of a notice's detail: an optional translated label (a gate step's kind), then data shown as it is. */
export interface DetailLine {
  label?: Message
  text?: string
}

export type TimelineItem =
  | (Base & { kind: 'lead' } & LeadMessageData)
  | (Base & { kind: 'route' } & RouteData)
  | (Base & { kind: 'prompt'; length: number; preview: string })
  | (Base & { kind: 'text'; text: string })
  | (Base & {
      kind: 'tool'
      tool: WorkerTool
      file?: { path: string; op: 'read' | 'edit' }
    })
  | (Base & { kind: 'file'; path: string; op: 'read' | 'edit' })
  | (Base & { kind: 'report'; report: WorkerReport })
  | (Base & { kind: 'turnEnd' } & TurnEndData)
  | (Base & {
      kind: 'notice'
      tone: Tone
      icon: IconName
      message: Message
      /** ISO time shown next to the message ("until …"). */
      until?: string
      /** A second message after the first ("2 files at risk"). */
      note?: Message
      /** Data shown under the line as it is (paths, an error, the worker's question); never daemon prose. */
      detail?: string
      /** Detail lines with translated labels (gate steps), shown instead of detail. */
      lines?: DetailLine[]
    })

export type TimelineKind = TimelineItem['kind']

type Draft = TimelineItem & { step?: number }

/** What folding needs to remember between events. */
interface Fold {
  items: Draft[]
  /** Open tool calls by id. */
  open: Map<string, Draft>
  /** Files at risk not restored yet, per turn. */
  atRisk: Map<number, Set<string>>
  /** The turn of a work_at_risk notice shown since the last attention or restored event. */
  riskShown: number | undefined
  /** The attention reason a gate or verify line just told (gate_red, verifier_rejected), with its turn. */
  flagShown: { reason: string; turn: number } | undefined
}

interface Extra {
  until?: string
  note?: Message
  detail?: string
  lines?: DetailLine[]
}

/** Attention reasons are snake_case words; anything else (older daemons sent prose) has no label. */
const REASON = /^[a-z][a-z_]*$/

/** The icon of an attention state. */
const ATTENTION_ICON: Partial<Record<string, IconName>> = {
  blocked: 'warning',
  info: 'info',
}

const openRisk = (f: Fold) => [...f.atRisk.values()].reduce((sum, files) => sum + files.size, 0)

const tokens = (usage: Usage | null | undefined) => usage?.totalTokens ?? 0

/** Folds one event into the items; returns the item it touched, or null when the event is not shown. */
function fold(f: Fold, event: TaskEvent): Draft | null {
  const base: Base = {
    key: `e${event.seq}`,
    rev: event.seq,
    turn: event.turn,
    attempt: event.attempt,
    ts: event.ts,
  }
  const notice = (
    tone: Tone,
    icon: IconName,
    key: string,
    params?: Message['params'],
    extra: Extra = {},
  ): Draft => ({ ...base, kind: 'notice', tone, icon, message: { key, params }, ...extra })

  switch (event.type) {
    case 'lead_message':
      return { ...base, kind: 'lead', ...event.data }
    case 'route':
      return { ...base, kind: 'route', ...event.data }
    case 'worker_prompt':
      return { ...base, kind: 'prompt', ...event.data }
    case 'turn_end':
      return { ...base, kind: 'turnEnd', ...event.data }
    case 'attempt_end':
      return event.data.status === 'ok' || event.data.status === 'cancelled'
        ? null
        : notice(
            'warn',
            'warning',
            `terminal.ev.attempt.${event.data.status}`,
            {},
            {
              detail: event.data.error,
            },
          )
    case 'quota_exhausted':
      return notice(
        'warn',
        'quota',
        'terminal.ev.quotaExhausted',
        {},
        {
          until: event.data.until,
          detail: event.data.error,
        },
      )
    case 'auth_failed':
      return notice('error', 'accounts', 'terminal.ev.authFailed', {}, { detail: event.data.error })
    case 'waiting_quota':
      return notice(
        'warn',
        'statusWaiting',
        'terminal.ev.waitingQuota',
        {},
        { until: event.data.until },
      )
    case 'retry':
      return notice(
        'warn',
        'refresh',
        'terminal.ev.retry',
        {
          seconds: Math.max(1, Math.round(event.data.delayMs / 1000)),
        },
        { detail: event.data.error },
      )
    case 'handoff':
      return notice(
        'info',
        'accounts',
        'terminal.ev.handoff',
        {
          from: event.data.from,
          to: event.data.to,
        },
        { detail: event.data.reason },
      )
    case 'reattach':
    case 'resumed':
      // A notice (the mask list could not be read, so the output is masked by key patterns) makes it a warning.
      return notice(
        event.data.notice ? 'warn' : 'dim',
        'refresh',
        `terminal.ev.${event.type}`,
        {},
        { detail: event.data.notice },
      )
    case 'attention':
      return foldAttention(f, event, base)
    case 'deps':
      return notice(
        event.data.state === 'blocked' ? 'error' : 'dim',
        'graph',
        `terminal.ev.deps.${event.data.state}`,
        { after: (event.data.after ?? []).join(', ') },
        { detail: event.data.reason },
      )
    case 'takeover':
      return notice('info', 'hand', `terminal.ev.takeover.${event.data.state}`)
    case 'handback':
      return notice('info', 'hand', 'terminal.ev.handback', {}, { detail: event.data.response })
    case 'review':
      return notice(
        event.data.verdict === 'accepted' ? 'ok' : 'warn',
        event.data.verdict === 'accepted' ? 'statusAccepted' : 'statusRejected',
        `terminal.ev.review.${event.data.verdict}`,
        {},
        { detail: event.data.notes },
      )
    case 'cancel':
      return notice('dim', 'statusCancelled', 'terminal.ev.cancel')
    case 'work_at_risk': {
      const turn = event.turn ?? 0
      f.atRisk.set(turn, new Set([...(f.atRisk.get(turn) ?? []), ...event.data.files]))
      f.riskShown = turn
      return notice(
        'warn',
        'warning',
        'terminal.ev.workAtRisk',
        { count: event.data.files.length },
        { detail: event.data.files.join('\n') },
      )
    }
    case 'restored': {
      const files = event.data.files
      const left = f.atRisk.get(event.turn ?? 0)
      for (const file of files) left?.delete(file)
      f.riskShown = undefined
      return files.length
        ? notice(
            'ok',
            'check',
            'terminal.ev.restored',
            { count: files.length, n: event.turn ?? '' },
            { detail: files.join('\n') },
          )
        : null
    }
    case 'gate':
    case 'lane':
    case 'verify': {
      const n =
        event.type === 'gate'
          ? gateNotice(event)
          : event.type === 'lane'
            ? laneNotice(event)
            : verifyNotice(event)
      if (!n) return null
      const flag = shownFlag(event)
      if (flag) f.flagShown = { reason: flag, turn: event.turn ?? 0 }
      return notice(n.tone, n.icon, n.message.key, n.message.params, {
        note: n.note,
        detail: n.detail,
        lines: n.lines,
      })
    }
    case 'error':
      return notice('error', 'error', 'terminal.ev.error', {}, { detail: event.data.message })
    case 'worker':
      return foldWorker(f.items, f.open, event, base, notice)
  }
}

/**
 * An attention flag. Its line names the reason (common.attention.*, the words of the task's pill) and falls back
 * to the state; the daemon's English detail is never shown, only the worker's own question or what the transcript
 * itself knows (the files still at risk). A work_at_risk flag right after that turn's work_at_risk line says
 * nothing new and is left out, and so is a gate_red or verifier_rejected flag right after that turn's failed
 * gate or rejecting review.
 */
function foldAttention(
  f: Fold,
  event: Extract<TaskEvent, { type: 'attention' }>,
  base: Base,
): Draft | null {
  const data = event.data
  const riskShown = f.riskShown
  const flagShown = f.flagShown
  f.riskShown = undefined
  f.flagShown = undefined
  const line = (tone: Tone, icon: IconName, message: Message, extra: Extra = {}): Draft => ({
    ...base,
    kind: 'notice',
    tone,
    icon,
    message,
    ...extra,
  })
  if (data.state === 'cleared') return line('dim', 'check', { key: 'terminal.ev.attentionCleared' })
  if (data.reason === 'work_at_risk' && riskShown !== undefined && riskShown === event.turn)
    return null
  if (flagShown && flagShown.reason === data.reason && flagShown.turn === (event.turn ?? 0))
    return null
  const state = `terminal.ev.attention.${data.state}`
  const message: Message = REASON.test(data.reason)
    ? { key: `common.attention.${reasonKey(data.reason)}`, fallback: state }
    : { key: state }
  const extra: Extra = {}
  if (data.reason === 'question' && data.detail) extra.detail = data.detail
  if (data.reason === 'work_at_risk') {
    const count = openRisk(f)
    if (count) extra.note = { key: 'terminal.ev.atRisk', params: { count } }
  }
  return line(
    data.state === 'info' ? 'dim' : 'warn',
    ATTENTION_ICON[data.state] ?? 'question',
    message,
    extra,
  )
}

/** The attention reason a gate or verify event already tells: a failed gate or a rejecting review. */
function shownFlag(event: TaskEvent): string | null {
  if (event.type === 'gate' && event.data.phase === 'result') {
    const state = event.data.result?.state
    return state === 'red' || state === 'error' ? 'gate_red' : null
  }
  if (event.type === 'verify' && event.data.result?.verdict === 'reject') return 'verifier_rejected'
  return null
}

function foldWorker(
  items: Draft[],
  open: Map<string, Draft>,
  event: Extract<TaskEvent, { type: 'worker' }>,
  base: Base,
  notice: (
    tone: Tone,
    icon: IconName,
    key: string,
    params?: Message['params'],
    extra?: object,
  ) => Draft,
): Draft | null {
  const data = event.data
  const scope = `${event.turn ?? 0}.${event.attempt ?? 0}`
  const step = data.step
  switch (data.kind) {
    case 'message': {
      const last = items.at(-1)
      if (
        last?.kind === 'text' &&
        last.turn === event.turn &&
        last.attempt === event.attempt &&
        last.step === step
      ) {
        last.text += data.text ?? ''
        last.rev = event.seq
        return last
      }
      if (!data.text) return null
      return { ...base, kind: 'text', text: data.text, step }
    }
    case 'tool_call':
    case 'tool_result': {
      const tool = data.tool
      if (!tool) return null
      const id = tool.id ? `${scope}#${tool.id}` : `${scope}@${tool.name}`
      const known = open.get(id)
      if (known?.kind === 'tool') {
        known.tool = { ...known.tool, ...tool, input: tool.input ?? known.tool.input }
        known.rev = event.seq
        if (tool.state !== 'calling') open.delete(id)
        return known
      }
      const item: Draft = { ...base, kind: 'tool', tool, step }
      if (tool.state === 'calling') open.set(id, item)
      return item
    }
    case 'file': {
      const file = data.file
      if (!file) return null
      // The file a read/edit tool touched belongs to that tool's item.
      for (let i = items.length - 1; i >= Math.max(0, items.length - 4); i--) {
        const item = items[i]
        if (item?.kind !== 'tool' || item.step !== step || item.file) continue
        if (file.tool && file.tool !== item.tool.name) continue
        item.file = { path: file.path, op: file.op }
        item.rev = event.seq
        return item
      }
      return { ...base, kind: 'file', path: file.path, op: file.op }
    }
    case 'report':
      return data.report ? { ...base, kind: 'report', report: data.report } : null
    case 'error':
      return data.error
        ? notice(
            'error',
            'warning',
            `terminal.ev.worker.${data.error.class}`,
            {},
            {
              detail: data.error.message,
            },
          )
        : null
    case 'background_wait':
      return notice('dim', 'statusWaiting', 'terminal.ev.backgroundWait', {}, { detail: data.text })
    case 'log':
      return data.text
        ? notice('dim', 'terminal', 'terminal.ev.log', {}, { detail: data.text })
        : null
    case 'session':
      return notice('dim', 'terminal', 'terminal.ev.session', {
        model: data.session?.model ?? '',
      })
    case 'usage':
      return null
  }
}

/** Builds the items for `events` (ascending seq), reusing objects from `previous` that did not change. */
export function buildTimeline(
  events: readonly TaskEvent[],
  previous: readonly TimelineItem[] = [],
): TimelineItem[] {
  const f: Fold = {
    items: [],
    open: new Map(),
    atRisk: new Map(),
    riskShown: undefined,
    flagShown: undefined,
  }
  const items = f.items
  for (const event of events) {
    const touched = fold(f, event)
    // A new item carries this event's key; an older item that changed keeps its own.
    if (touched?.key === `e${event.seq}`) items.push(touched)
  }
  if (!previous.length) return items
  const before = new Map(previous.map((item) => [item.key, item]))
  return items.map((item) => {
    const old = before.get(item.key)
    return old && old.rev === item.rev && old.kind === item.kind ? old : item
  })
}

export function sameTimeline(a: readonly TimelineItem[], b: readonly TimelineItem[]): boolean {
  return a.length === b.length && a.every((item, i) => item === b[i])
}

/** Total tokens of a finished turn, for its footer. */
export const turnTokens = (item: Extract<TimelineItem, { kind: 'turnEnd' }>) => tokens(item.usage)
