import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/api/client'
import type { RolesApi, RoleWrite } from '@/api/roles'
import type { Role } from '@/api/types'

import { RoleMoveError, useRolesStore } from './roles'

const role = (scope: Role['scope'], cwd?: string): Role => ({
  id: 'reviewer',
  name: 'Reviewer',
  description: '',
  rules: '',
  outputStyle: '',
  skills: [],
  preferredAccounts: [],
  scope,
  ...(cwd ? { cwd } : {}),
  overridesGlobal: false,
})

const write: RoleWrite = {
  role: {
    id: 'reviewer',
    name: 'Reviewer',
    description: '',
    rules: '',
    outputStyle: '',
    skills: [],
    preferredAccounts: [],
  },
  scope: 'project',
  cwd: 'D:/Work/app',
} as RoleWrite

function store(remove: RolesApi['remove']) {
  const api = {
    list: vi.fn(() => Promise.resolve({ items: [], projects: [] })),
    save: vi.fn(() => Promise.resolve(role('project', 'D:/Work/app'))),
    remove: vi.fn(remove),
  }
  const roles = useRolesStore()
  roles.setApi(api as unknown as RolesApi)
  return { roles, api }
}

describe('roles store: moving a role (E-B26)', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('removes the new copy again and throws the delete error when the old copy cannot go', async () => {
    const { roles, api } = store((_id, scope) =>
      scope === 'global'
        ? Promise.reject(new ApiError(500, 'internal', 'disk full'))
        : Promise.resolve(),
    )
    await expect(roles.save(write, { id: 'reviewer', scope: 'global' })).rejects.toThrow(
      'disk full',
    )
    expect(api.remove).toHaveBeenLastCalledWith('reviewer', 'project', 'D:/Work/app')
  })

  it('says the role is in both places when removing the new copy fails too', async () => {
    const { roles } = store((_id, scope) =>
      Promise.reject(new ApiError(500, 'internal', scope === 'global' ? 'disk full' : 'locked')),
    )
    const err = await roles.save(write, { id: 'reviewer', scope: 'global' }).catch((e) => e)
    expect(err).toBeInstanceOf(RoleMoveError)
    expect((err as RoleMoveError).message).toContain('disk full')
    expect((err as RoleMoveError).message).toContain('locked')
    expect((err as RoleMoveError).from).toEqual({ id: 'reviewer', scope: 'global' })
  })

  it('counts a 404 on the rollback as removed', async () => {
    const { roles } = store((_id, scope) =>
      Promise.reject(
        scope === 'global'
          ? new ApiError(500, 'internal', 'disk full')
          : new ApiError(404, 'not_found', 'gone'),
      ),
    )
    const err = await roles.save(write, { id: 'reviewer', scope: 'global' }).catch((e) => e)
    expect(err).not.toBeInstanceOf(RoleMoveError)
    expect((err as Error).message).toBe('disk full')
  })
})
