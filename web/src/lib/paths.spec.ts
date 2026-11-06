import { describe, expect, it } from 'vitest'

import { baseName, dirName, relativeTo } from './paths'

describe('paths', () => {
  it('shows paths inside the task folder relative to it', () => {
    expect(relativeTo('D:\\Work\\app\\src\\a.ts', 'D:/Work/app')).toBe('src/a.ts')
    expect(relativeTo('d:/work/APP/src/a.ts', 'D:/Work/app/')).toBe('src/a.ts')
    expect(relativeTo('/home/me/app/a.ts', '/home/me/app')).toBe('a.ts')
    expect(relativeTo('D:/Work/app', 'D:/Work/app')).toBe('.')
  })

  it('keeps paths outside the folder, and case matters off Windows', () => {
    expect(relativeTo('D:/Work/application/a.ts', 'D:/Work/app')).toBe('D:/Work/application/a.ts')
    expect(relativeTo('/home/me/App/a.ts', '/home/me/app')).toBe('/home/me/App/a.ts')
    expect(relativeTo('src/a.ts', undefined)).toBe('src/a.ts')
  })

  it('splits names and folders', () => {
    expect(baseName('pages/[id].vue')).toBe('[id].vue')
    expect(baseName('a\\b\\c.go')).toBe('c.go')
    expect(dirName('pages/[id].vue')).toBe('pages')
    expect(dirName('README.md')).toBe('')
  })
})
