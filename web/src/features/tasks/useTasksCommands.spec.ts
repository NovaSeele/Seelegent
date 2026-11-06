import { describe, expect, it } from 'vitest'

import { iso, makeTask } from '@/test/fixtures'

import { findTasks, nextNeed } from './useTasksCommands'

describe('task commands', () => {
  const tasks = [
    makeTask({ id: 't-old', title: 'Sửa lỗi đăng nhập', createdAt: iso(-2000) }),
    makeTask({ id: 't-new', title: 'Login page copy', createdAt: iso(-1000) }),
    makeTask({ id: 't-x', title: 'Unrelated', projectName: 'shop' }),
  ]

  it('finds tasks by every word, ignoring the palette prefix and diacritics', () => {
    expect(findTasks(tasks, 'Task: loi dang', 'Task:').map((t) => t.id)).toEqual(['t-old'])
    expect(findTasks(tasks, 'shop', 'Task:').map((t) => t.id)).toEqual(['t-x'])
    expect(findTasks(tasks, 'Task:', 'Task:')).toEqual([])
    expect(findTasks(tasks, 't-', 'Task:', 2).map((t) => t.id)).toEqual(['t-x', 't-new'])
  })

  it('steps to the next need and wraps around', () => {
    expect(nextNeed(['a', 'b', 'c'], undefined)).toBe('a')
    expect(nextNeed(['a', 'b', 'c'], 'b')).toBe('c')
    expect(nextNeed(['a', 'b', 'c'], 'c')).toBe('a')
    expect(nextNeed(['a', 'b'], 'zzz')).toBe('a')
    expect(nextNeed([], 'a')).toBeUndefined()
  })
})
