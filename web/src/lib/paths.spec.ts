import { describe, expect, it } from 'vitest'

import { baseName, dirName, joinPath, pathKey, relativeTo } from './paths'

describe('paths', () => {
  it('keys folders like the daemon: separators, trailing slash, and case only on Windows', () => {
    expect(pathKey('D:\\Work\\App\\')).toBe(pathKey('d:/work/app'))
    expect(pathKey('/home/me/App')).not.toBe(pathKey('/home/me/app'))
    expect(pathKey('/home/me/app/')).toBe('/home/me/app')
    expect(pathKey('/')).toBe('/')
  })

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

  it('joins and resolves dot segments', () => {
    expect(joinPath('D:/mono/web', '../shared/x.ts')).toBe('D:/mono/shared/x.ts')
    expect(joinPath('D:\\app\\', './src//a.ts')).toBe('D:/app/src/a.ts')
    expect(joinPath('/home/me', '../../../etc')).toBe('/etc')
  })

  it('splits names and folders', () => {
    expect(baseName('pages/[id].vue')).toBe('[id].vue')
    expect(baseName('a\\b\\c.go')).toBe('c.go')
    expect(dirName('pages/[id].vue')).toBe('pages')
    expect(dirName('README.md')).toBe('')
  })
})
