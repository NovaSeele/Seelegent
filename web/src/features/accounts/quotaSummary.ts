/**
 * The quota summary of an account row: at most two windows of the quota group the pool routes this backend's work
 * on, so a glance at the list says whether the account can take work.
 *
 * Which group: Account carries only group names (no model list per group), so a group counts as a routing group
 * when its name matches one of the backend's tier models (Settings.backends[backend].tiers). The name is split on
 * " and " into alternatives ("Claude and GPT models" → claude | gpt); an alternative matches a model when each of
 * its words, minus "model(s)", begins one of the model id's words. So for Antigravity with Gemini Flash tiers,
 * "Gemini Models" matches gemini-3.8-flash while "Claude and GPT models" and "Gemini 4 Argon" do not: draining
 * those in the Antigravity app never shows as an empty bar on an account that still takes work. For Claude Code,
 * "Claude" and the sub-limits of the tier families ("Claude Opus" for claude-opus-5) match.
 *
 * Which windows: of the matching groups, the lowest short window (a day or less, e.g. 5h) and the lowest long one
 * (weekly), then the lowest others to fill two. When no group matches (tiers unset, a renamed group), the summary
 * falls back to the single lowest window of all groups, marked `routing: false`: the row draws it in a neutral
 * colour and says it is the lowest of all windows. `more` counts the windows not shown.
 */
import type { Account, QuotaWindow, TierModels } from '@/api/types'

export interface SummaryWindow {
  group: string
  window: QuotaWindow
}

export interface QuotaSummary {
  windows: SummaryWindow[]
  /** True when the windows come from the routing group; false for the lowest-of-all fallback. */
  routing: boolean
  /** Windows of the account not shown in the summary. */
  more: number
}

const DAY = 24 * 60

const KEY_MINUTES: Readonly<Record<string, number>> = {
  daily: DAY,
  day: DAY,
  weekly: 7 * DAY,
  week: 7 * DAY,
  monthly: 30 * DAY,
  month: 30 * DAY,
}

/** A window's length in minutes, from its minutes or its key ("5h", "30m", "7d", "weekly"); 0 when unknown. */
export function windowMinutes(window: QuotaWindow): number {
  if (window.minutes && window.minutes > 0) return window.minutes
  const key = window.window.trim().toLowerCase()
  const named = KEY_MINUTES[key]
  if (named) return named
  const match = /^(\d+)([mhd])$/.exec(key)
  if (!match) return 0
  const n = Number(match[1])
  return match[2] === 'm' ? n : match[2] === 'h' ? n * 60 : n * DAY
}

const words = (text: string): string[] =>
  text
    .toLowerCase()
    .split(/[^a-z0-9]+/)
    .filter(Boolean)

const FILLER = new Set(['model', 'models'])

/** Does the quota group `name` govern `model`? (See the file comment.) */
export function groupMatchesModel(name: string, model: string): boolean {
  const modelWords = words(model)
  return name
    .toLowerCase()
    .split(/\s+and\s+/)
    .map((alt) => words(alt).filter((w) => !FILLER.has(w)))
    .some(
      (alt) =>
        alt.length > 0 && alt.every((w) => modelWords.some((m) => m === w || m.startsWith(w))),
    )
}

function lowest(list: readonly SummaryWindow[]): SummaryWindow | undefined {
  let best: SummaryWindow | undefined
  for (const item of list) if (!best || item.window.remaining < best.window.remaining) best = item
  return best
}

export function quotaSummary(account: Account, tiers: TierModels | undefined): QuotaSummary {
  const all: SummaryWindow[] = account.quota.groups.flatMap((g) =>
    g.windows.map((window) => ({ group: g.name, window })),
  )
  if (!all.length) return { windows: [], routing: false, more: 0 }

  const models = tiers ? Object.values(tiers).filter((m): m is string => !!m) : []
  const routed = all.filter((w) => models.some((m) => groupMatchesModel(w.group, m)))
  if (!routed.length) {
    const only = lowest(all) as SummaryWindow
    return { windows: [only], routing: false, more: all.length - 1 }
  }

  const short = lowest(
    routed.filter((w) => windowMinutes(w.window) > 0 && windowMinutes(w.window) <= DAY),
  )
  const long = lowest(routed.filter((w) => windowMinutes(w.window) > DAY))
  const picked = [short, long].filter((w): w is SummaryWindow => !!w)
  const rest = routed.filter((w) => !picked.includes(w))
  while (picked.length < 2 && rest.length) {
    const next = lowest(rest) as SummaryWindow
    picked.push(next)
    rest.splice(rest.indexOf(next), 1)
  }
  // Shorter window first, so "5h" sits before "weekly" in every row.
  picked.sort((a, b) => windowMinutes(a.window) - windowMinutes(b.window))
  return { windows: picked, routing: true, more: all.length - picked.length }
}
