/** Typed endpoints of worker roles (contract §5). */
import type { ApiClient } from './client'
import type { Role, RoleList, RoleScope, Skill } from './types'

/** The role fields a write carries; scope and cwd travel next to it. */
export type RoleFields = Omit<Role, 'scope' | 'cwd' | 'overridesGlobal' | 'createdAt' | 'updatedAt'>

/** POST /roles stores a role in one scope: global, or the project of `cwd`. The same id replaces. */
export interface RoleWrite {
  role: RoleFields
  scope: RoleScope
  cwd?: string
}

export function createRolesApi(client: ApiClient) {
  return {
    /** `all` lists every project's roles; `cwd` one project's roles with the global ones. */
    list: (query: { cwd?: string; all?: boolean } = {}, signal?: AbortSignal) =>
      client.get<RoleList>('/roles', {
        signal,
        query: { cwd: query.cwd, all: query.all ? 1 : undefined },
      }),
    templates: () => client.get<Role[]>('/roles/templates'),
    skills: (cwd?: string) => client.get<Skill[]>('/roles/skills', { query: { cwd } }),
    save: (body: RoleWrite) => client.post<Role>('/roles', { body }),
    /** `project`: the project's folder or its key (RoleList.projects[].key). */
    remove: (id: string, scope: RoleScope, project?: string) =>
      client.delete<void>(`/roles/${encodeURIComponent(id)}`, {
        query: { scope, cwd: scope === 'project' ? project : undefined },
      }),
  }
}

export type RolesApi = ReturnType<typeof createRolesApi>
