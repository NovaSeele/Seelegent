import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick, ref, shallowRef } from 'vue'

import { ApiError, type GitApi } from '@/api'
import type { FileDiff, GitFile, TaskGit, TaskSummary } from '@/api/types'
import { useToastStore } from '@/stores/toast'
import { flush, setupApp } from '@/test/app'
import { makeTask } from '@/test/fixtures'

import CommitDialog from './CommitDialog.vue'
import {
  committable,
  MAX_DRAFTS,
  putDraft,
  sortFiles,
  splitPath,
  stepFile,
  totals,
  type Drafts,
} from './git'
import { useGit, type GitModel } from './useGit'

const file = (path: string, extra: Partial<GitFile> = {}): GitFile => ({
  path,
  status: 'modified',
  additions: 1,
  deletions: 1,
  binary: false,
  ...extra,
})

const repo = (files: GitFile[], extra: Partial<TaskGit> = {}): TaskGit => ({
  repo: true,
  root: 'D:/mono',
  branch: 'main',
  dirty: files.length > 0,
  files,
  preexisting: [],
  suggestion: null,
  ...extra,
})

const emptyDiff = (path: string): FileDiff => ({ path, binary: false, truncated: false, hunks: [] })

function fakeGitApi(overrides: Partial<GitApi> = {}): GitApi {
  const missing = () => Promise.reject(new Error('unexpected call'))
  return {
    status: vi.fn(missing),
    diff: vi.fn((_id: string, path: string) => Promise.resolve(emptyDiff(path))),
    suggestMessage: vi.fn(missing),
    dismissSuggestion: vi.fn(missing),
    commit: vi.fn(missing),
    outsideDiff: vi.fn(missing),
    ...overrides,
  }
}

describe('git model', () => {
  const files = sortFiles([
    file('src/b.ts'),
    file('packages/web/pages/[id].vue', { status: 'added', additions: 9, deletions: 0 }),
    file('packages/web/components/Card.vue', { status: 'renamed', oldPath: 'components/Card.vue' }),
  ])

  it('sorts by repo path and sums the counts', () => {
    expect(files.map((f) => f.path)).toEqual([
      'packages/web/components/Card.vue',
      'packages/web/pages/[id].vue',
      'src/b.ts',
    ])
    expect(totals(files)).toEqual({ additions: 11, deletions: 2 })
    expect(splitPath('packages/web/pages/[id].vue')).toEqual({
      dir: 'packages/web/pages/',
      name: '[id].vue',
    })
    expect(splitPath('go.mod')).toEqual({ dir: '', name: 'go.mod' })
  })

  it('steps through files, clamped, starting at the first', () => {
    expect(stepFile(files, null, 1)).toBe('packages/web/components/Card.vue')
    expect(stepFile(files, 'src/b.ts', 1)).toBe('src/b.ts')
    expect(stepFile(files, 'src/b.ts', -1)).toBe('packages/web/pages/[id].vue')
    expect(stepFile([], null, 1)).toBeNull()
  })

  it('commits only ticked paths that are still among the files, in list order', () => {
    expect(
      committable(files, new Set(['src/b.ts', 'gone.ts', 'packages/web/pages/[id].vue'])),
    ).toEqual(['packages/web/pages/[id].vue', 'src/b.ts'])
  })

  it('keeps the newest drafts and drops blank ones', () => {
    let drafts: Drafts = {}
    for (let i = 0; i < MAX_DRAFTS + 3; i++) drafts = putDraft(drafts, `t${i}`, `m${i}`, i)
    expect(Object.keys(drafts)).toHaveLength(MAX_DRAFTS)
    expect(drafts.t0).toBeUndefined()
    expect(drafts[`t${MAX_DRAFTS + 2}`]?.message).toBe(`m${MAX_DRAFTS + 2}`)
    expect(putDraft(drafts, 't5', '  ', 99).t5).toBeUndefined()
  })
})

describe('useGit', () => {
  let model!: GitModel

  async function setup(api: GitApi, initial: Partial<TaskSummary> = {}) {
    const { plugins } = await setupApp()
    const task = shallowRef(makeTask({ id: 't1', status: 'done', ...initial }))
    mount(
      defineComponent({
        setup() {
          model = useGit(() => task.value, api)
          return () => h('div')
        },
      }),
      { global: { plugins } },
    )
    await flush(4)
    return task
  }

  beforeEach(() => {
    model = undefined as unknown as GitModel
  })

  it('shows a failed read as an error, never as "not a repository" (B11)', async () => {
    const status = vi
      .fn<GitApi['status']>()
      .mockRejectedValueOnce(new ApiError(502, 'upstream', 'git status timed out'))
      .mockResolvedValueOnce({
        repo: false,
        dirty: false,
        files: [],
        preexisting: [],
        suggestion: null,
      })
    await setup(fakeGitApi({ status }))
    expect(model.state.value).toEqual({ status: 'error', error: 'git status timed out' })
    await model.reload()
    expect(model.state.value).toMatchObject({ status: 'ready', git: { repo: false } })
  })

  it('keeps the last good data when a refresh fails', async () => {
    const status = vi
      .fn<GitApi['status']>()
      .mockResolvedValueOnce(repo([file('a.ts')]))
      .mockRejectedValueOnce(new Error('offline'))
    const task = await setup(fakeGitApi({ status }))
    task.value = { ...task.value, filesChanged: 2 }
    await flush(4)
    expect(status).toHaveBeenCalledTimes(2)
    expect(model.state.value).toMatchObject({ status: 'ready', refreshError: 'offline' })
    expect(model.files.value.map((f) => f.path)).toEqual(['a.ts'])
  })

  it('selects the first file and re-reads its diff only when the file changes', async () => {
    const status = vi
      .fn<GitApi['status']>()
      .mockResolvedValueOnce(repo([file('b.ts'), file('a.ts')]))
      .mockResolvedValueOnce(repo([file('b.ts', { additions: 5 }), file('a.ts')]))
    const api = fakeGitApi({ status })
    const task = await setup(api)
    expect(model.selected.value).toBe('a.ts')
    expect(api.diff).toHaveBeenCalledTimes(1)
    expect(model.diff.value).toMatchObject({ status: 'ready', path: 'a.ts' })
    task.value = { ...task.value, filesChanged: 3 }
    await flush(4)
    expect(api.diff).toHaveBeenCalledTimes(1)
    model.selected.value = 'b.ts'
    await flush(4)
    expect(api.diff).toHaveBeenLastCalledWith('t1', 'b.ts', undefined, expect.any(AbortSignal))
  })

  it('re-reads while a suggestion is being written, on task updates only', async () => {
    const running = repo([file('a.ts')], { suggestion: { state: 'running' } })
    const ready = repo([file('a.ts')], { suggestion: { state: 'ready', message: 'fix: a' } })
    const status = vi
      .fn<GitApi['status']>()
      .mockResolvedValueOnce(repo([file('a.ts')]))
      .mockResolvedValueOnce(ready)
    const api = fakeGitApi({ status, suggestMessage: vi.fn(() => Promise.resolve(running)) })
    const task = await setup(api)
    await model.suggest()
    expect(model.suggestion.value).toEqual({ state: 'running' })
    task.value = { ...task.value, updatedAt: '2026-11-06T10:05:00.000Z' }
    await flush(4)
    expect(model.suggestion.value).toEqual({ state: 'ready', message: 'fix: a' })
    task.value = { ...task.value, updatedAt: '2026-11-06T10:06:00.000Z' }
    await flush(4)
    expect(status).toHaveBeenCalledTimes(2)
  })

  it('commits exactly the given paths and reports a refusal once, in place', async () => {
    const commit = vi
      .fn<GitApi['commit']>()
      .mockRejectedValueOnce(new ApiError(409, 'bad_state', 'task is running'))
      .mockResolvedValueOnce({ sha: 'abc1234', summary: '1 file changed' })
    const status = vi.fn<GitApi['status']>(() =>
      Promise.resolve(repo([file('a.ts'), file('b.ts')])),
    )
    await setup(fakeGitApi({ status, commit }))
    const toasts = useToastStore()
    expect(await model.commit('fix: a', ['a.ts'])).toEqual({ ok: false, error: 'task is running' })
    expect(toasts.toasts).toHaveLength(0)
    const outcome = await model.commit('fix: a', ['a.ts'])
    expect(outcome.ok).toBe(true)
    expect(commit).toHaveBeenLastCalledWith('t1', 'fix: a', ['a.ts'])
    expect(toasts.toasts.map((t) => t.message)).toEqual(['Committed abc1234: 1 file changed'])
  })
})

describe('CommitDialog', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  it('sends the ticked files and the message; fills an empty message from a suggestion', async () => {
    const { plugins } = await setupApp()
    const files = [
      file('a.ts'),
      file('pkg/[id].vue', { status: 'renamed', oldPath: 'old/[id].vue' }),
    ]
    const commit = vi.fn<GitModel['commit']>(() =>
      Promise.resolve({ ok: true as const, result: { sha: 'abc', summary: '' } }),
    )
    const open = ref(false)
    const message = ref('')
    const suggestion = shallowRef<TaskGit['suggestion']>({ state: 'running' })
    const host = () =>
      h(CommitDialog, {
        files,
        suggestion: suggestion.value,
        busy: null,
        active: false,
        commit,
        open: open.value,
        'onUpdate:open': (v: boolean) => (open.value = v),
        message: message.value,
        'onUpdate:message': (v: string) => (message.value = v),
      })
    mount(host, { attachTo: document.body, global: { plugins } })
    open.value = true
    await flush()
    expect(document.body.textContent).toContain('renamed file is committed together')
    suggestion.value = { state: 'ready', message: 'refactor: move card' }
    await flush()
    expect(message.value).toBe('refactor: move card')
    // The message already is the suggestion: no Use / Dismiss row repeating it.
    expect(document.querySelector('.cd__suggestion')).toBeNull()
    message.value = 'refactor: move the card'
    await flush()
    expect(document.querySelector('.cd__suggestion')?.textContent).toContain('refactor: move card')
    message.value = 'refactor: move card'
    await flush()

    const boxes = [...document.querySelectorAll<HTMLInputElement>('.cd__list input')]
    boxes[0]?.click()
    await flush()
    const confirm = [...document.querySelectorAll('button')].find((b) =>
      b.textContent?.includes('Commit 1 file'),
    )
    expect(confirm).toBeDefined()
    confirm?.click()
    await flush()
    expect(commit).toHaveBeenCalledWith('refactor: move card', ['pkg/[id].vue'])
    await nextTick()
    expect(open.value).toBe(false)
    expect(message.value).toBe('')
  })
})
