/** Pure helpers of the Roles page: ids from names, the scope filter, and the editor's draft. */
import type { Effort, Role, RoleScope, Tier } from '@/api/types'
import type { RoleFields } from '@/api/roles'
import { pathKey } from '@/lib/paths'

export const ROLE_ID = /^[a-z0-9][a-z0-9-]{0,39}$/
export const TIERS: readonly Tier[] = ['fast', 'standard', 'strong', 'max']
export const EFFORTS: readonly Effort[] = ['low', 'medium', 'high', 'xhigh', 'max']
export const LIMITS = { name: 60, description: 300, rules: 8000, outputStyle: 2000, skills: 20 }

/** "Front-end Lead (Vue)" → "front-end-lead-vue"; accents dropped, at most 40 characters, never empty. */
export function slugify(name: string): string {
  const slug = name
    .normalize('NFKD')
    .replace(/[̀-ͯ]/g, '')
    .replace(/đ/gi, 'd')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 40)
    .replace(/-+$/, '')
  return slug || 'role'
}

/** '' = every role, 'global' = global ones, otherwise a project folder. */
export type ScopeFilter = '' | 'global' | string

export function filterRoles(roles: readonly Role[], filter: ScopeFilter): Role[] {
  if (filter === '') return [...roles]
  if (filter === 'global') return roles.filter((r) => r.scope === 'global')
  return roles.filter((r) => r.scope === 'project' && pathKey(r.cwd ?? '') === pathKey(filter))
}

/** Global roles first, then by project folder, then by name. */
export function sortRoles(roles: readonly Role[]): Role[] {
  return [...roles].sort(
    (a, b) =>
      (a.scope === 'global' ? 0 : 1) - (b.scope === 'global' ? 0 : 1) ||
      (a.cwd ?? '').localeCompare(b.cwd ?? '') ||
      a.name.localeCompare(b.name),
  )
}

export interface RoleDraft {
  id: string
  name: string
  description: string
  rules: string
  outputStyle: string
  skills: string[]
  defaultTier: Tier | ''
  defaultEffort: Effort | ''
  preferredAccounts: string[]
  scope: RoleScope
  cwd: string
}

export function draftOf(role: Role | undefined, scope: RoleScope = 'global', cwd = ''): RoleDraft {
  return {
    id: role?.id ?? '',
    name: role?.name ?? '',
    description: role?.description ?? '',
    rules: role?.rules ?? '',
    outputStyle: role?.outputStyle ?? '',
    skills: [...(role?.skills ?? [])],
    defaultTier: role?.defaultTier ?? '',
    defaultEffort: role?.defaultEffort ?? '',
    preferredAccounts: [...(role?.preferredAccounts ?? [])],
    scope: role?.scope ?? scope,
    cwd: role?.cwd ?? cwd,
  }
}

/** The fields a write carries; empty tier and effort are left out (no default). */
export function fieldsOf(draft: RoleDraft): RoleFields {
  return {
    id: draft.id,
    name: draft.name.trim(),
    description: draft.description.trim(),
    rules: draft.rules,
    outputStyle: draft.outputStyle,
    skills: draft.skills,
    ...(draft.defaultTier ? { defaultTier: draft.defaultTier } : {}),
    ...(draft.defaultEffort ? { defaultEffort: draft.defaultEffort } : {}),
    preferredAccounts: draft.preferredAccounts,
  }
}
