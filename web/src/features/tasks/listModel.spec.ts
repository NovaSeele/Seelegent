import { describe, expect, it } from 'vitest'

import { iso, makeTask, T0 } from '@/test/fixtures'

import {
  apiQuery,
  buildRows,
  fold,
  listKey,
  matches,
  NEEDS_GROUP,
  rowKey,
  sameRows,
  type ListFilters,
  type ListRow,
} from './listModel'

const NONE: ListFilters = { q: '', statuses: [], project: '' }
const filters = (over: Partial<ListFilters>): ListFilters => ({ ...NONE, ...over })

const shop = { projectKey: 'd:/work/shop', projectName: 'shop' }
const app = { projectKey: 'd:/work/app', projectName: 'app' }

describe('search and filters', () => {
  it('folds case and diacritics, including đ', () => {
    expect(fold('Sửa LỖI đăng nhập')).toBe('sua loi dang nhap')
    expect(fold('Café')).toBe('cafe')
  })

  it('matches every word against title, id, project or account', () => {
    const task = makeTask({
      id: 't2611-ab',
      title: 'Sửa lỗi đăng nhập',
      ...shop,
      accountLabel: 'Work',
    })
    expect(matches(task, filters({ q: 'loi shop' }), T0)).toBe(true)
    expect(matches(task, filters({ q: 'ab work' }), T0)).toBe(true)
    expect(matches(task, filters({ q: 'loi mobile' }), T0)).toBe(false)
  })

  it('filters by project and by any of the chosen statuses', () => {
    const running = makeTask({ id: 'r', ...shop })
    const review = makeTask({ id: 'v', status: 'done', ...shop })
    const reviewed = makeTask({
      id: 'd',
      status: 'done',
      review: { verdict: 'accepted', notes: '', at: iso() },
    })
    const cancelled = makeTask({ id: 'c', status: 'cancelled' })
    const pick = (f: ListFilters) =>
      [running, review, reviewed, cancelled].filter((t) => matches(t, f, T0)).map((t) => t.id)
    expect(pick(filters({ project: shop.projectKey }))).toEqual(['r', 'v'])
    expect(pick(filters({ statuses: ['review'] }))).toEqual(['v'])
    expect(pick(filters({ statuses: ['done', 'cancelled'] }))).toEqual(['d', 'c'])
    expect(pick(filters({ statuses: ['active'] }))).toEqual(['r'])
    // A task waiting for review needs you; it is not "active".
    expect(pick(filters({ statuses: ['needs'] }))).toEqual(['v'])
  })

  it('asks the server for a superset only when every filter maps to statuses', () => {
    expect(
      apiQuery(filters({ statuses: ['failed', 'cancelled'], project: 'p', q: ' x ' })),
    ).toEqual({ status: ['blocked_deps', 'cancelled', 'failed'], project: 'p', q: 'x' })
    // "needs" depends on attention and age, so the server cannot narrow by status.
    expect(apiQuery(filters({ statuses: ['needs', 'failed'] }))).toEqual({})
  })
})

describe('rows', () => {
  const tasks = [
    makeTask({ id: 'a1', ...app, createdAt: iso(-3000), updatedAt: iso(-3000) }),
    makeTask({ id: 's1', ...shop, createdAt: iso(-2000), updatedAt: iso(-1000) }),
    makeTask({ id: 'a2', ...app, createdAt: iso(-1000), updatedAt: iso(-2000) }),
    makeTask({
      id: 'q',
      ...app,
      attention: { state: 'question', since: iso(), reason: 'question', who: 'user' },
    }),
  ]

  it('pins the tasks that need you, then groups projects by latest activity', () => {
    const rows = buildRows(tasks, NONE, 'project', new Set(), T0)
    expect(rows.map(rowKey)).toEqual([
      `g:${NEEDS_GROUP}`,
      't:q',
      'g:d:/work/shop',
      't:s1',
      'g:d:/work/app',
      't:a2',
      't:a1',
    ])
    expect(rows[2]).toMatchObject({ kind: 'group', label: 'shop', count: 1 })
  })

  it('hides the members of a collapsed group but keeps its count', () => {
    const rows = buildRows(tasks, NONE, 'project', new Set(['d:/work/app']), T0)
    expect(rows.map(rowKey)).toEqual([
      `g:${NEEDS_GROUP}`,
      't:q',
      'g:d:/work/shop',
      't:s1',
      'g:d:/work/app',
    ])
    expect(rows.at(-1)).toMatchObject({ collapsed: true, count: 2 })
  })

  it('lists newest first without groups when flat', () => {
    const rows = buildRows(tasks, NONE, 'flat', new Set(), T0)
    expect(rows.map(rowKey)).toEqual(['t:q', 't:a2', 't:s1', 't:a1'])
  })

  it('compares row lists by content', () => {
    const a = buildRows(tasks, NONE, 'project', new Set(), T0)
    const b = buildRows([...tasks].reverse(), NONE, 'project', new Set(), T0)
    expect(sameRows(a, b)).toBe(true)
    expect(sameRows(a, buildRows(tasks, NONE, 'flat', new Set(), T0))).toBe(false)
  })
})

describe('keyboard walk', () => {
  const rows: ListRow[] = [
    { kind: 'group', key: 'p1', label: 'one', count: 2, collapsed: false },
    { kind: 'task', id: 'x', group: 'p1' },
    { kind: 'task', id: 'y', group: 'p1' },
    { kind: 'group', key: 'p2', label: 'two', count: 3, collapsed: true },
  ]

  it('moves up and down within bounds', () => {
    expect(listKey(rows, null, 'ArrowDown')).toEqual({ focus: 'g:p1' })
    expect(listKey(rows, 't:x', 'ArrowDown')).toEqual({ focus: 't:y' })
    expect(listKey(rows, 'g:p2', 'ArrowDown')).toEqual({ focus: 'g:p2' })
    expect(listKey(rows, 'g:p1', 'ArrowUp')).toEqual({ focus: 'g:p1' })
    expect(listKey(rows, 't:x', 'End')).toEqual({ focus: 'g:p2' })
    expect(listKey(rows, 't:y', 'Home')).toEqual({ focus: 'g:p1' })
  })

  it('collapses, expands and steps like a tree', () => {
    expect(listKey(rows, 'g:p1', 'ArrowLeft')).toEqual({ collapse: 'p1' })
    expect(listKey(rows, 't:y', 'ArrowLeft')).toEqual({ focus: 'g:p1' })
    expect(listKey(rows, 'g:p2', 'ArrowLeft')).toBeNull()
    expect(listKey(rows, 'g:p2', 'ArrowRight')).toEqual({ expand: 'p2' })
    expect(listKey(rows, 'g:p1', 'ArrowRight')).toEqual({ focus: 't:x' })
    expect(listKey(rows, 't:x', 'ArrowRight')).toBeNull()
    expect(listKey([], null, 'ArrowDown')).toBeNull()
  })
})
