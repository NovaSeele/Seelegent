/** Pure helpers of the meetings and contests pages: status tones and icons, and form parsing. */
import type { Citation, ContestSummary, Meeting, MeetingSummary } from '@/api/types'
import type { IconName } from '@/components/ui/icons'
import type { Tone } from '@/components/ui/tone'

interface Look {
  tone: Tone
  icon: IconName
}

/** Meeting states: running rounds look like running tasks; a meeting that waits on the lead asks for attention. */
export const MEETING_STATUS: Record<MeetingSummary['status'], Look> = {
  round: { tone: 'running', icon: 'statusRunning' },
  waiting_lead: { tone: 'attention', icon: 'statusBlocked' },
  deciding: { tone: 'attention', icon: 'statusReview' },
  dissent: { tone: 'running', icon: 'statusRunning' },
  closed: { tone: 'success', icon: 'statusAccepted' },
  cancelled: { tone: 'muted', icon: 'statusCancelled' },
  failed: { tone: 'danger', icon: 'statusFailed' },
}

export const CONTEST_STATUS: Record<ContestSummary['status'], Look> = {
  starting: { tone: 'running', icon: 'statusRunning' },
  running: { tone: 'running', icon: 'statusRunning' },
  judging: { tone: 'running', icon: 'statusRunning' },
  ready: { tone: 'attention', icon: 'statusReview' },
  applied: { tone: 'success', icon: 'statusAccepted' },
  cancelled: { tone: 'muted', icon: 'statusCancelled' },
  failed: { tone: 'danger', icon: 'statusFailed' },
}

/** A citation checked against the repository is trusted; one only located is neutral; the rest are wrong. */
export function citeTone(state: Citation['state']): Tone {
  if (state === 'verified') return 'success'
  if (state === 'located') return 'neutral'
  return 'danger'
}

export function citeText(c: Citation): string {
  return c.endLine && c.endLine !== c.line
    ? `${c.path}:${c.line}-${c.endLine}`
    : `${c.path}:${c.line}`
}

/** One entry per non-empty line, trimmed. */
export function lines(text: string): string[] {
  return text
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter(Boolean)
}

/** Questions to the lead that have no answer yet. */
export function pendingQuestions(m: Meeting): Meeting['leadQa'] {
  // Only a meeting waiting for the lead takes answers; questions it went on without stay unanswered.
  if (m.status !== 'waiting_lead') return []
  return m.leadQa.filter((q) => !q.answer)
}

/** One question to the lead as the form shows it: identical questions from several attendees are one. */
export interface QuestionGroup {
  /** The ids of every copy: one answer goes to all of them. */
  ids: string[]
  labels: string[]
  question: string
}

/** Pending questions merged by their text (trimmed, without case), in the order first asked. */
export function questionGroups(m: Meeting): QuestionGroup[] {
  const groups = new Map<string, QuestionGroup>()
  for (const q of pendingQuestions(m)) {
    const key = q.question.trim().toLowerCase()
    const g = groups.get(key)
    if (g) {
      g.ids.push(q.id)
      if (!g.labels.includes(q.label)) g.labels.push(q.label)
    } else {
      groups.set(key, { ids: [q.id], labels: [q.label], question: q.question.trim() })
    }
  }
  return [...groups.values()]
}

/** The member behind an attendee label (P1..), for showing who said what. */
export function attendeeName(m: Meeting, label: string): string {
  const a = m.attendees.find((x) => x.label === label)
  if (!a) return label
  return `${label} · ${a.name || a.member}`
}

/** A meeting that still runs or waits can be cancelled. */
export function meetingOpen(status: MeetingSummary['status']): boolean {
  return status !== 'closed' && status !== 'cancelled' && status !== 'failed'
}

/** A contest that has not ended can be cancelled. */
export function contestOpen(status: ContestSummary['status']): boolean {
  return status !== 'applied' && status !== 'cancelled' && status !== 'failed'
}
