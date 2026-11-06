/**
 * Layout of the Graph view: one lane per stage, time running left to right, each step a bar as wide as it took
 * (at least `minWidth`). Idle stretches longer than `gapMs` are cut to a fixed `gapPx` break, so a task that
 * waited an hour for quota still fits; turns and later attempts get a mark where they start.
 */
import { STAGES, type Stage, type Step, type TurnNode } from './process'

export interface GraphOptions {
  pxPerSec: number
  gapMs: number
  gapPx: number
  minWidth: number
  /** Left padding before the first bar. */
  pad: number
}

export const GRAPH_DEFAULTS: GraphOptions = {
  pxPerSec: 6,
  gapMs: 20_000,
  gapPx: 28,
  minWidth: 6,
  pad: 12,
}

export interface Bar {
  step: Step
  lane: number
  x: number
  w: number
}

export interface Mark {
  x: number
  turn: number
  attempt: number
  /** Account of a later attempt (a hand-over). */
  account?: string
}

export interface GraphLayout {
  width: number
  bars: Bar[]
  marks: Mark[]
  /** x of each compressed idle gap. */
  breaks: number[]
  /** Real time covered, idle gaps included. */
  spanMs: number
}

export const laneOf = (stage: Stage) => STAGES.indexOf(stage)

/** `durationOf` lets the caller measure running steps against the clock. */
export function layoutGraph(
  turns: readonly TurnNode[],
  options: GraphOptions = GRAPH_DEFAULTS,
  durationOf: (step: Step) => number = (step) => step.durationMs,
): GraphLayout {
  const { pxPerSec, gapMs, gapPx, minWidth, pad } = options
  const px = (ms: number) => (ms / 1000) * pxPerSec
  const entries = turns.flatMap((turn) =>
    turn.attempts.flatMap((attempt) =>
      attempt.steps.map((step) => ({
        step,
        turn: turn.n,
        attempt: attempt.n,
        account: attempt.accountLabel,
      })),
    ),
  )
  entries.sort((a, b) => a.step.start - b.step.start)

  const bars: Bar[] = []
  const marks: Mark[] = []
  const breaks: number[] = []
  const first = entries[0]
  if (!first) return { width: pad * 2, bars, marks, breaks, spanMs: 0 }

  let x = pad
  let t = first.step.start
  let last = { turn: -1, attempt: -1 }
  for (const { step, turn, attempt, account } of entries) {
    if (step.start > t) {
      const gap = step.start - t
      if (gap > gapMs) {
        breaks.push(x + gapPx / 2)
        x += gapPx
      } else {
        x += px(gap)
      }
      t = step.start
    }
    // A step that started while an earlier one was still running sits back in time from the cursor.
    const left = Math.max(pad, x - px(t - step.start))
    const duration = Math.max(0, durationOf(step))
    if (turn !== last.turn || attempt !== last.attempt) {
      marks.push({ x: left, turn, attempt, account: attempt > 1 ? account : undefined })
      last = { turn, attempt }
    }
    bars.push({ step, lane: laneOf(step.stage), x: left, w: Math.max(minWidth, px(duration)) })
    const end = step.start + duration
    if (end > t) {
      x += px(end - t)
      t = end
    }
  }
  const right = Math.max(x, ...bars.map((bar) => bar.x + bar.w))
  const spanMs = t - first.step.start
  return { width: Math.ceil(right + pad), bars, marks, breaks, spanMs }
}
