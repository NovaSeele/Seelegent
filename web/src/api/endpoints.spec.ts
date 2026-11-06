import { describe, expect, it, vi } from 'vitest'

import { createApiClient } from './client'
import { createGitApi } from './git'
import { createTasksApi } from './tasks'

function setup(body: unknown = {}, status = 200) {
  const fetch = vi.fn<typeof globalThis.fetch>(
    async () =>
      new Response(JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json' },
      }),
  )
  const client = createApiClient({ fetch })
  const call = (i = 0) => {
    const [url, init] = fetch.mock.calls[i] ?? []
    const headers = (init?.headers ?? {}) as Record<string, string>
    return {
      url: String(url),
      method: init?.method,
      headers,
      body: init?.body ? (JSON.parse(String(init.body)) as unknown) : undefined,
    }
  }
  return { tasks: createTasksApi(client), git: createGitApi(client), call }
}

describe('task endpoints', () => {
  it('lists with comma statuses and drops empty filters', async () => {
    const { tasks, call } = setup({ items: [] })
    await tasks.list({ status: ['running', 'queued'], project: '', q: '  login ', limit: 50 })
    expect(call().url).toBe('/api/v1/tasks?status=running%2Cqueued&q=login&limit=50')
    await tasks.list({ cursor: 'c2' })
    expect(call(1).url).toBe('/api/v1/tasks?cursor=c2')
  })

  it('reads events either forward from a seq or as a tail, never both', async () => {
    const { tasks, call } = setup({ items: [], lastSeq: 0 })
    await tasks.events('t/1', { tail: 500 })
    expect(call().url).toBe('/api/v1/tasks/t%2F1/events?tail=500')
    await tasks.events('t1', { after: 12, limit: 100 })
    expect(call(1).url).toBe('/api/v1/tasks/t1/events?after=12&limit=100')
  })

  it('sends writes with X-Seelegent and a JSON body', async () => {
    const { tasks, call } = setup({ id: 't1' })
    await tasks.followup('t1', { message: 'again', tier: 'strong' })
    expect(call()).toMatchObject({
      url: '/api/v1/tasks/t1/followup',
      method: 'POST',
      headers: { 'X-Seelegent': '1', 'Content-Type': 'application/json' },
      body: { message: 'again', tier: 'strong' },
    })
    await tasks.review('t1', 'rejected', '  fix tests ')
    expect(call(1).body).toEqual({ verdict: 'rejected', notes: 'fix tests' })
    await tasks.review('t1', 'accepted', '   ')
    expect(call(2).body).toEqual({ verdict: 'accepted' })
    await tasks.cancel('t1')
    expect(call(3).body).toEqual({})
    await tasks.cancel('t1', 15)
    expect(call(4).body).toEqual({ waitSeconds: 15 })
    await tasks.answer('t1', 'yes')
    expect(call(5)).toMatchObject({ url: '/api/v1/tasks/t1/answer', body: { answer: 'yes' } })
    await tasks.dismissAttention('t1')
    expect(call(6).url).toBe('/api/v1/tasks/t1/attention/dismiss')
    await tasks.takeover('t1')
    expect(call(7).url).toBe('/api/v1/tasks/t1/takeover')
  })

  it('surfaces contract errors as ApiError', async () => {
    const { tasks } = setup(
      { error: { code: 'bad_state', message: 'task is running', details: { status: 'running' } } },
      409,
    )
    await expect(tasks.followup('t1', { message: 'x' })).rejects.toMatchObject({
      name: 'ApiError',
      status: 409,
      code: 'bad_state',
      message: 'task is running',
    })
  })
})

describe('git endpoints', () => {
  it('reads a file diff by path and optional turn', async () => {
    const { git, call } = setup({ path: 'a.ts', binary: false, truncated: false, hunks: [] })
    await git.diff('t1', 'src/a b.ts')
    expect(call().url).toBe('/api/v1/tasks/t1/git/diff?path=src%2Fa+b.ts')
    await git.diff('t1', 'a.ts', 2)
    expect(call(1).url).toBe('/api/v1/tasks/t1/git/diff?path=a.ts&turn=2')
  })

  it('commits exactly the chosen files', async () => {
    const { git, call } = setup({ sha: 'abc1234', summary: '2 files' })
    const result = await git.commit('t1', 'feat: x', ['a.ts', 'b.ts'])
    expect(result.sha).toBe('abc1234')
    expect(call()).toMatchObject({
      url: '/api/v1/tasks/t1/git/commit',
      method: 'POST',
      body: { message: 'feat: x', files: ['a.ts', 'b.ts'] },
    })
    await git.suggestMessage('t1')
    expect(call(1).url).toBe('/api/v1/tasks/t1/git/suggest-message')
    await git.dismissSuggestion('t1')
    expect(call(2).url).toBe('/api/v1/tasks/t1/git/suggest-message/dismiss')
  })
})
