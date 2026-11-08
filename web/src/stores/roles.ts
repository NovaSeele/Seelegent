/**
 * Worker roles of every scope (GET /roles?all=1), the templates and the installed skills per project. Read when
 * the Roles page first needs them, again on each roles.changed frame and after a resync.
 *
 * Moving a role to another scope (or renaming its id) is a write to the new place and then a delete of the old
 * one; the contract has no single route for it. When the delete fails, the new copy is deleted again, so the
 * role is never left in both places (old bug B26). A 404 on that delete means the old copy is already gone: the
 * move is done, nothing is rolled back. When removing the new copy fails too, the role is in both places and the
 * save throws a RoleMoveError that says so (E-B26).
 */
import { defineStore } from 'pinia'
import { shallowRef } from 'vue'

import { rolesApi as defaultApi } from '@/api'
import { ApiError, errorMessage } from '@/api/client'
import type { RoleFields, RolesApi, RoleWrite } from '@/api/roles'
import { roleScope } from '@/api/types'
import type { Role, RoleProject, Skill } from '@/api/types'
import { pathKey } from '@/lib/paths'

import { useConnectionStore } from './connection'
import { createLoader } from './loader'

/**
 * Where a role lives: its scope ('global' or 'project'; Role.scope is a plain string) and, for a project role,
 * the project's folder.
 */
export interface RolePlace {
  id: string
  scope: Role['scope']
  cwd?: string
}

export const samePlace = (a: RolePlace, b: RolePlace): boolean =>
  a.id === b.id &&
  a.scope === b.scope &&
  (a.scope === 'global' || pathKey(a.cwd ?? '') === pathKey(b.cwd ?? ''))

/**
 * A move whose old copy could not be deleted (`reason`) and whose new copy could not be removed again either
 * (`rollback`): the role is now at both `from` and `to`.
 */
export class RoleMoveError extends Error {
  override readonly name = 'RoleMoveError'

  constructor(
    readonly reason: unknown,
    readonly rollback: unknown,
    readonly from: RolePlace,
    readonly to: RolePlace,
  ) {
    super(
      `${errorMessage(reason)}; removing the new copy failed too (${errorMessage(rollback)}): ` +
        'the role is in both places',
    )
  }
}

/** A project a role can be placed in: its daemon key (fsutil.ProjectKey), folder and name. */
export interface ProjectChoice {
  key: string
  cwd: string
  name: string
}

/**
 * Projects to offer for a project role: every project the daemon knows (/state) plus those that already have
 * roles (GET /roles lists only those), once per project. Both lists carry the daemon's project key (the path
 * normalised, case-insensitive on Windows), so that key decides what is the same project, never the cwd text.
 */
export function projectChoices(
  known: readonly ProjectChoice[],
  withRoles: readonly ProjectChoice[],
): ProjectChoice[] {
  const seen = new Set<string>()
  const out: ProjectChoice[] = []
  for (const p of [...known, ...withRoles]) {
    if (!p.cwd) continue
    const key = p.key || pathKey(p.cwd)
    if (seen.has(key)) continue
    seen.add(key)
    out.push({ key, cwd: p.cwd, name: p.name })
  }
  return out
}

/** The fields of a role a write carries. */
export function roleFields(role: Role): RoleFields {
  return {
    id: role.id,
    name: role.name,
    description: role.description,
    rules: role.rules,
    outputStyle: role.outputStyle,
    skills: role.skills,
    defaultTier: role.defaultTier,
    defaultEffort: role.defaultEffort,
    preferredAccounts: role.preferredAccounts,
  }
}

export const useRolesStore = defineStore('roles', () => {
  let client: RolesApi = defaultApi
  const items = shallowRef<readonly Role[]>([])
  const projects = shallowRef<readonly RoleProject[]>([])
  const templates = shallowRef<readonly Role[]>([])
  /** Skills per project folder ('' = no project). */
  const skills = shallowRef<Readonly<Record<string, readonly Skill[]>>>({})
  const { status, error, busy, load } = createLoader(
    (signal) => client.list({ all: true }, signal),
    (list) => {
      items.value = list.items
      projects.value = list.projects
    },
  )
  let watching = false

  function setApi(next: RolesApi): void {
    client = next
  }

  function ensure(): void {
    if (watching) return
    watching = true
    const connection = useConnectionStore()
    connection.subscribe('roles.changed', () => void load())
    connection.onResync(() => void load())
    void load()
  }

  async function loadTemplates(): Promise<void> {
    if (templates.value.length === 0) templates.value = await client.templates()
  }

  /**
   * The skills a role in `cwd` can use (cached per folder; `fresh` reads them again, since skills are installed
   * outside the dashboard); throws when they cannot be read.
   */
  async function loadSkills(cwd = '', fresh = false): Promise<readonly Skill[]> {
    const cached = skills.value[cwd]
    if (cached && !fresh) return cached
    const list = await client.skills(cwd || undefined)
    skills.value = { ...skills.value, [cwd]: list }
    return list
  }

  function find(place: RolePlace): Role | undefined {
    return items.value.find((role) => samePlace(role, place))
  }

  /**
   * Saves `write`. With `from` (the role as it was) at another place, the old one is deleted after the new one is
   * stored; when that delete fails the new copy is removed again and the error is thrown, or a RoleMoveError when
   * the new copy cannot be removed either.
   */
  async function save(write: RoleWrite, from?: RolePlace): Promise<Role> {
    const to: RolePlace = { id: write.role.id, scope: write.scope, cwd: write.cwd }
    const saved = await client.save(write)
    if (from && !samePlace(from, to)) {
      try {
        await removeIfThere(from)
      } catch (cause) {
        let stuck: RoleMoveError | null = null
        try {
          await removeIfThere(to)
        } catch (rollback) {
          stuck = new RoleMoveError(cause, rollback, from, to)
        }
        await load()
        throw stuck ?? cause
      }
    }
    await load()
    return saved
  }

  /** DELETE that counts a 404 (someone deleted it already) as done. */
  async function removeIfThere(place: RolePlace): Promise<void> {
    try {
      await client.remove(place.id, roleScope(place.scope), place.cwd)
    } catch (cause) {
      if (!(cause instanceof ApiError && cause.status === 404)) throw cause
    }
  }

  async function remove(place: RolePlace): Promise<void> {
    await removeIfThere(place)
    items.value = items.value.filter((role) => !samePlace(role, place))
    void load()
  }

  return {
    items,
    projects,
    templates,
    skills,
    status,
    error,
    busy,
    setApi,
    ensure,
    load,
    loadTemplates,
    loadSkills,
    find,
    save,
    remove,
  }
})
