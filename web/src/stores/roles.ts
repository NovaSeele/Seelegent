/**
 * Worker roles of every scope (GET /roles?all=1), the templates and the installed skills per project. Read when
 * the Roles page first needs them, again on each roles.changed frame and after a resync.
 *
 * Moving a role to another scope (or renaming its id) is a write to the new place and then a delete of the old
 * one; the contract has no single route for it. When the delete fails, the new copy is deleted again, so the
 * role is never left in both places (old bug B26).
 */
import { defineStore } from 'pinia'
import { shallowRef } from 'vue'

import { rolesApi as defaultApi } from '@/api'
import type { RoleFields, RolesApi, RoleWrite } from '@/api/roles'
import type { Role, RoleProject, Skill } from '@/api/types'

import { useConnectionStore } from './connection'
import { createLoader } from './loader'

/** Where a role lives: its scope and, for a project role, the project's folder. */
export interface RolePlace {
  id: string
  scope: Role['scope']
  cwd?: string
}

export const samePlace = (a: RolePlace, b: RolePlace): boolean =>
  a.id === b.id && a.scope === b.scope && (a.scope === 'global' || a.cwd === b.cwd)

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

  /** The skills a role in `cwd` can use (cached per folder); throws when they cannot be read. */
  async function loadSkills(cwd = ''): Promise<readonly Skill[]> {
    const cached = skills.value[cwd]
    if (cached) return cached
    const list = await client.skills(cwd || undefined)
    skills.value = { ...skills.value, [cwd]: list }
    return list
  }

  function find(place: RolePlace): Role | undefined {
    return items.value.find((role) => samePlace(role, place))
  }

  /**
   * Saves `write`. With `from` (the role as it was) at another place, the old one is deleted after the new one is
   * stored; when that delete fails the new copy is removed again and the error is thrown.
   */
  async function save(write: RoleWrite, from?: RolePlace): Promise<Role> {
    const to: RolePlace = { id: write.role.id, scope: write.scope, cwd: write.cwd }
    const saved = await client.save(write)
    if (from && !samePlace(from, to)) {
      try {
        await client.remove(from.id, from.scope, from.cwd)
      } catch (cause) {
        await client.remove(to.id, to.scope, to.cwd).catch(() => undefined)
        await load()
        throw cause
      }
    }
    await load()
    return saved
  }

  async function remove(place: RolePlace): Promise<void> {
    await client.remove(place.id, place.scope, place.cwd)
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
