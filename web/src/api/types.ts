/**
 * HTTP API v1 data shapes. Every DTO comes from types.gen.ts, which `go generate ./internal/api` writes from the Go
 * structs in internal/api (a Go test fails when it is stale); this module re-exports them, so the app imports
 * '@/api/types' and never the generated file directly. Never edit types.gen.ts by hand.
 *
 * What stays hand-written here is only what the generator cannot express, each with its reason below: a
 * TypeScript-only mapped type (SettingsPatch), string unions the Go side keeps as plain strings (Effort,
 * RoleScope) with their narrowing helpers, and the hook shapes of a later milestone (not in internal/api yet).
 * The TaskEvent union and the SSE frame types live in events.ts for the same reason.
 */
import type { Settings, Tier } from './types.gen'

export type * from './types.gen'

/**
 * A JSON merge patch of Settings (RFC 7396) for PATCH /settings: nested objects merge, arrays are replaced whole,
 * and `null` resets that key to its default. Hand-written: a mapped type over Settings, which Go has no way to
 * describe (the daemon decodes the patch as raw JSON).
 */
export type SettingsPatch = MergePatch<Settings>

type MergePatch<T> = {
  [K in keyof T]?:
    (T[K] extends readonly unknown[] ? T[K] : T[K] extends object ? MergePatch<T[K]> : T[K]) | null
}

/** Every tier, in order (the generated Tier union as a list). */
export const TIERS: readonly Tier[] = ['fast', 'standard', 'strong', 'max']

/** Narrows a plain string (Role.defaultTier, Settings.defaultTier) to a Tier. */
export function isTier(value: string | undefined): value is Tier {
  return (TIERS as readonly (string | undefined)[]).includes(value)
}

/**
 * Reasoning effort a role can default to. Hand-written: Go keeps Role.defaultEffort (and every effort field) a
 * plain string, because each backend accepts its own set; the role editor offers this list.
 */
export type Effort = 'low' | 'medium' | 'high' | 'xhigh' | 'max'

export const EFFORTS: readonly Effort[] = ['low', 'medium', 'high', 'xhigh', 'max']

/** Narrows a plain string (Role.defaultEffort) to an Effort. */
export function isEffort(value: string | undefined): value is Effort {
  return (EFFORTS as readonly (string | undefined)[]).includes(value)
}

/**
 * Where a role is stored. Hand-written: Go keeps Role.scope a plain string (RoleRequest.scope is the generated
 * union); the roles page needs the union to pick the DELETE query.
 */
export type RoleScope = 'global' | 'project'

/** Narrows Role.scope; anything that is not 'project' is treated as a global role. */
export function roleScope(value: string): RoleScope {
  return value === 'project' ? 'project' : 'global'
}

// Hooks (contract §5). Hand-written: the daemon answers every /hooks route with 501 until milestone 14, which pins
// the shape in internal/api; until then this is the old hooks.json entry plus the stable id the contract requires.

export interface HookConfig {
  /** Stable id; '' for a hook not saved yet. */
  id: string
  /** Event names, e.g. task.done, account.exhausted. */
  on: string[]
  /** A shell command. */
  run: string
  /** Only tasks in this folder fire the hook. */
  project?: string
  timeoutSeconds: number
  enabled: boolean
}

export interface HookRun {
  hookId: string
  event: string
  at: string
  exitCode?: number
  durationMs: number
  timedOut: boolean
}

export interface Hook extends HookConfig {
  lastRun: HookRun | null
}
