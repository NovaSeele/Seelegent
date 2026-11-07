/**
 * Transcript notices of the gate, lane and verify events (docs/gates.md). Only what the events carry is shown as
 * detail: step labels, commands, their output tail, paths and the verifier's reasons.
 */
import type { TaskEvent } from '@/api/events'
import type { GateResult } from '@/api/types'
import type { IconName } from '@/components/ui/icons'

import type { Message, Tone } from './timeline'

export interface GateNotice {
  tone: Tone
  icon: IconName
  message: Message
  note?: Message
  detail?: string
}

type Of<T extends string> = Extract<TaskEvent, { type: T }>

const GATE_TONE: Record<GateResult['state'], Tone> = {
  running: 'dim',
  green: 'ok',
  red: 'error',
  error: 'error',
  skipped: 'dim',
}

const GATE_ICON: Record<GateResult['state'], IconName> = {
  running: 'gates',
  green: 'check',
  red: 'warning',
  error: 'error',
  skipped: 'statusCancelled',
}

/** The failing steps of a gate as plain lines: label, command and output tail. */
export function failingDetail(result: GateResult): string {
  return result.steps
    .filter((s) => (s.state === 'red' || s.state === 'error') && !s.foreign)
    .map((s) => {
      const head = s.cmd && s.cmd !== s.label ? `${s.label}: ${s.cmd}` : s.label
      const files = s.files?.length ? `\n${s.files.join('\n')}` : ''
      return `${head}${files}${s.tail ? `\n${s.tail.trimEnd()}` : ''}`
    })
    .join('\n\n')
}

export function gateNotice(event: Of<'gate'>): GateNotice | null {
  const data = event.data
  switch (data.phase) {
    case 'start':
      return {
        tone: 'dim',
        icon: 'gates',
        message: { key: 'terminal.ev.gate.start', params: { count: data.steps?.length ?? 0 } },
        detail: data.steps?.join('\n'),
      }
    case 'settling':
      return { tone: 'warn', icon: 'statusWaiting', message: { key: 'terminal.ev.gate.settling' } }
    case 'result': {
      const result = data.result
      if (!result) return null
      return {
        tone: GATE_TONE[result.state],
        icon: GATE_ICON[result.state],
        message: { key: `terminal.ev.gate.${result.state}` },
        note: result.autoFix
          ? { key: 'terminal.ev.gate.autoFix', params: { n: result.autoFix } }
          : undefined,
        detail: failingDetail(result) || undefined,
      }
    }
    default:
      return null // step progress shows in the gate panel
  }
}

export function laneNotice(event: Of<'lane'>): GateNotice {
  const data = event.data
  const lines =
    data.kind === 'violation'
      ? (data.files ?? [])
      : (data.conflicts ?? []).map(
          (c) => `${c.taskId} · ${c.title}: ${c.paths.map((p) => p.theirs).join(', ')}`,
        )
  return {
    tone: data.kind === 'warning' ? 'warn' : 'error',
    icon: 'warning',
    message: { key: `terminal.ev.lane.${data.kind}`, params: { count: lines.length } },
    detail: lines.join('\n') || undefined,
  }
}

export function verifyNotice(event: Of<'verify'>): GateNotice | null {
  const data = event.data
  if (data.phase === 'start') {
    return { tone: 'info', icon: 'search', message: { key: 'terminal.ev.verify.start' } }
  }
  const result = data.result
  if (!result || result.verdict === 'running') return null
  return {
    tone: result.verdict === 'accept' ? 'ok' : result.verdict === 'reject' ? 'warn' : 'error',
    icon: result.verdict === 'accept' ? 'check' : 'warning',
    message: { key: `terminal.ev.verify.${result.verdict}` },
    detail: result.reasons.join('\n') || undefined,
  }
}
