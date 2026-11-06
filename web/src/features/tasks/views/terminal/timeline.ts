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

interface Base {
  key: string
  rev: number
  turn?: number
  ts: string
}

export type Tone = 'info' | 'ok' | 'warn' | 'error' | 'dim'

export interface Message {
  key: string
  params?: Record<string, string | number>
}

export type TimelineItem =
  | (Base & { kind: 'lead' } & LeadMessageData)
  | (Base & { kind: 'route' } & RouteData)
  | (Base & { kind: 'prompt'; attempt?: number; length: number; preview: string })
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
      detail?: string
    })

export type TimelineKind = TimelineItem['kind']

type Draft = TimelineItem & { step?: number; attempt?: number }

const tokens = (usage: Usage | null | undefined) => usage?.totalTokens ?? 0

/** Folds one event into the items; returns the item it touched, or null when the event is not shown. */
function fold(items: Draft[], open: Map<string, Draft>, event: TaskEvent): Draft | null {
  const base = { key: `e${event.seq}`, rev: event.seq, turn: event.turn, ts: event.ts }
  const notice = (
    tone: Tone,
    icon: IconName,
    key: string,
    params?: Message['params'],
    extra: { until?: string; detail?: string } = {},
  ): Draft => ({ ...base, kind: 'notice', tone, icon, message: { key, params }, ...extra })

  switch (event.type) {
    case 'lead_message':
      return { ...base, kind: 'lead', ...event.data }
    case 'route':
      return { ...base, kind: 'route', ...event.data }
    case 'worker_prompt':
      return { ...base, kind: 'prompt', attempt: event.attempt, ...event.data }
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
      return notice('dim', 'refresh', 'terminal.ev.reattach')
    case 'resumed':
      return notice('dim', 'refresh', 'terminal.ev.resumed')
    case 'attention':
      return event.data.state === 'cleared'
        ? notice('dim', 'check', 'terminal.ev.attentionCleared')
        : notice(
            'warn',
            'question',
            `terminal.ev.attention.${event.data.state}`,
            {},
            {
              detail: event.data.detail ?? event.data.reason,
            },
          )
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
    case 'work_at_risk':
      return notice(
        'error',
        'warning',
        'terminal.ev.workAtRisk',
        {
          count: event.data.files.length,
        },
        { detail: event.data.files.join('\n') },
      )
    case 'error':
      return notice('error', 'error', 'terminal.ev.error', {}, { detail: event.data.message })
    case 'worker':
      return foldWorker(items, open, event, base, notice)
  }
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
      return { ...base, kind: 'text', text: data.text, step, attempt: event.attempt }
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
      const item: Draft = { ...base, kind: 'tool', tool, step, attempt: event.attempt }
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
  const items: Draft[] = []
  const open = new Map<string, Draft>()
  for (const event of events) {
    const touched = fold(items, open, event)
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
