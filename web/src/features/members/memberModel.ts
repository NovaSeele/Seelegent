/** Pure helpers of the Members page: the editor's draft, what a save sends, and the order of the list. */
import type { MemberWrite } from '@/api/team'
import type { Member, MemberTrack, Rank, Tier } from '@/api/types'
import type { Tone } from '@/components/ui/tone'

export type Backend = Member['backend']

export const BACKENDS: readonly Backend[] = ['agy', 'claude', 'codex']
export const RANKS: readonly Rank[] = ['worker', 'senior', 'peer']
export const MEMBER_ID = /^[a-z0-9][a-z0-9-]{0,39}$/

/** Rank tones: a peer can block acceptance, so it stands out most. */
export const RANK_TONE: Record<Rank, Tone> = {
  worker: 'muted',
  senior: 'neutral',
  peer: 'attention',
}

/** "Peri Okafor" for a known member, else its id. */
export function memberLabel(members: readonly Member[], id: string): string {
  return members.find((m) => m.id === id)?.name || id
}

export interface MemberDraft {
  id: string
  name: string
  rank: Rank
  backend: Backend
  /** '' runs on the backend's accounts. */
  provider: string
  tier: Tier | ''
  model: string
  role: string
  fallbackBackends: Backend[]
  enabled: boolean
}

export function draftOf(m?: Member): MemberDraft {
  return {
    id: m?.id ?? '',
    name: m?.name ?? '',
    rank: m?.rank ?? 'worker',
    backend: m?.backend ?? 'agy',
    provider: m?.provider ?? '',
    tier: m?.tier ?? '',
    model: m?.model ?? '',
    role: m?.role ?? '',
    fallbackBackends: (m?.fallbackBackends ?? []).filter((b): b is Backend =>
      (BACKENDS as readonly string[]).includes(b),
    ),
    enabled: m?.enabled ?? true,
  }
}

/**
 * The body of a save. Every field is sent, so a cleared tier, model or role clears it (PATCH merges onto the stored
 * member). A provider member has no fallback backends, and its backend is the provider's.
 */
export function inputOf(d: MemberDraft, providerBackend?: Backend): MemberWrite {
  const provider = d.provider.trim()
  const backend = provider && providerBackend ? providerBackend : d.backend
  return {
    ...(d.id.trim() ? { id: d.id.trim() } : {}),
    name: d.name.trim(),
    rank: d.rank,
    backend,
    provider,
    tier: d.tier,
    model: d.model.trim(),
    role: d.role,
    fallbackBackends: provider ? [] : d.fallbackBackends.filter((b) => b !== backend),
    enabled: d.enabled,
  }
}

/** Enabled first, then by name. */
export function sortMembers(list: readonly Member[]): Member[] {
  return [...list].sort(
    (a, b) => Number(b.enabled) - Number(a.enabled) || a.name.localeCompare(b.name),
  )
}

/** The non-zero numbers of a track record, in a fixed order. */
export function trackFacts(
  track: MemberTrack | undefined,
): { key: keyof Omit<MemberTrack, 'member'>; value: number }[] {
  if (!track) return []
  const keys = [
    'meetings',
    'dissents',
    'vindicated',
    'adviceNotTaken',
    'consults',
    'objections',
  ] as const
  return keys.map((key) => ({ key, value: track[key] })).filter((f) => f.value > 0)
}
