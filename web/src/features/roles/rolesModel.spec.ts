import { describe, expect, it } from 'vitest'

import type { Role } from '@/api/types'

import { draftOf, fieldsOf, filterRoles, ROLE_ID, slugify, sortRoles } from './rolesModel'

const role = (overrides: Partial<Role>): Role => ({
  id: 'qa',
  name: 'QA',
  description: '',
  rules: '',
  outputStyle: '',
  skills: [],
  preferredAccounts: [],
  scope: 'global',
  overridesGlobal: false,
  ...overrides,
})

describe('roles model', () => {
  it('makes a valid id from any name', () => {
    expect(slugify('Front-end Lead (Vue)')).toBe('front-end-lead-vue')
    expect(slugify('Kiểm thử Đầu cuối')).toBe('kiem-thu-dau-cuoi')
    expect(slugify('测试')).toBe('role')
    expect(slugify('a'.repeat(50))).toHaveLength(40)
    for (const name of ['Docs', '  x  ', '---', 'Ünïcode Ränge'])
      expect(ROLE_ID.test(slugify(name))).toBe(true)
  })

  it('filters by scope and sorts global roles first', () => {
    const list = [
      role({ id: 'b', name: 'B', scope: 'project', cwd: 'D:/app' }),
      role({ id: 'a', name: 'A' }),
    ]
    expect(filterRoles(list, 'global').map((r) => r.id)).toEqual(['a'])
    expect(filterRoles(list, 'D:/app').map((r) => r.id)).toEqual(['b'])
    expect(sortRoles(list).map((r) => r.id)).toEqual(['a', 'b'])
  })

  it('leaves an unset tier and effort out of the write', () => {
    const fields = fieldsOf({ ...draftOf(role({})), name: ' QA ' })
    expect(fields.name).toBe('QA')
    expect('defaultTier' in fields).toBe(false)
    expect(fieldsOf({ ...draftOf(role({})), defaultTier: 'fast' }).defaultTier).toBe('fast')
  })

  // The generated Role keeps defaultTier, defaultEffort and scope as plain strings.
  it('narrows the plain-string fields of a role into the draft', () => {
    const known = draftOf(role({ defaultTier: 'strong', defaultEffort: 'high', scope: 'project' }))
    expect([known.defaultTier, known.defaultEffort, known.scope]).toEqual([
      'strong',
      'high',
      'project',
    ])
    const odd = draftOf(role({ defaultTier: 'turbo', defaultEffort: 'extreme', scope: 'team' }))
    expect([odd.defaultTier, odd.defaultEffort, odd.scope]).toEqual(['', '', 'global'])
  })
})
