import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { defineComponent, h, shallowRef, type PropType, type VNode } from 'vue'

import type { GitApi } from '@/api'
import type {
  FileDiff,
  GitCommit,
  GitCommitDetail,
  GitFile,
  RepoStatus,
  TaskGit,
} from '@/api/types'
import { flush, press, setupApp } from '@/test/app'

import GitCommitList from './GitCommitList.vue'
import GitHistory from './GitHistory.vue'
import GitRepoPanel from './GitRepoPanel.vue'
import GitStartNotice from './GitStartNotice.vue'
import GitUpstream from './GitUpstream.vue'
import { startNotices, useGitHistory, type GitHistoryModel } from './useGitHistory'
import { otherFiles, upstreamOf, useRepoStatus, type RepoStatusModel } from './useRepoStatus'

const START = 'a'.repeat(40)
const file = (path: string, extra: Partial<GitFile> = {}): GitFile => ({
  path,
  status: 'modified',
  additions: 2,
  deletions: 1,
  binary: false,
  ...extra,
})
const commit = (sha: string, subject: string, extra: Partial<GitCommit> = {}): GitCommit => ({
  sha: sha.repeat(40).slice(0, 40),
  subject,
  author: 'Ana',
  authorTime: '2026-11-08T10:00:00.000Z',
  turn: 1,
  fileCount: 1,
  ...extra,
})
const view = (extra: Partial<TaskGit> = {}): TaskGit => ({
  repo: true,
  root: 'D:/repo',
  branch: 'main',
  dirty: false,
  files: [],
  untrackedDirs: [],
  preexisting: [],
  suggestion: null,
  head: 'c'.repeat(40),
  startState: 'recorded',
  startHead: START,
  startBranch: 'main',
  branchChanged: false,
  commits: [],
  commitsMore: 0,
  committedFiles: [],
  ...extra,
})
const diffOf = (path: string, text = 'x'): FileDiff => ({
  path,
  binary: false,
  truncated: false,
  hunks: [
    {
      header: '@@ -0,0 +1 @@',
      oldStart: 0,
      oldLines: 0,
      newStart: 1,
      newLines: 1,
      lines: [{ kind: 'add', text, newNo: 1 }],
    },
  ],
})
const detailOf = (c: GitCommit, paths: string[]): GitCommitDetail => ({
  ...c,
  body: 'Why it changed.',
  parent: START,
  files: paths.map((p) => file(p)),
  diffs: paths.map((p) => diffOf(p, `in ${p}`)),
  truncated: false,
})

/** Runs `make` in a component's setup (composables need one) and renders what it returns, if anything. */
const Host = defineComponent({
  props: { make: { type: Function as PropType<() => (() => VNode) | void>, required: true } },
  setup(props) {
    const render = props.make()
    return () => (render ? render() : h('div'))
  },
})

function fakeApi(overrides: Partial<GitApi> = {}): GitApi {
  const missing = () => Promise.reject(new Error('unexpected call'))
  return {
    status: vi.fn(missing),
    diff: vi.fn((_id: string, path: string) => Promise.resolve(diffOf(path, 'since start'))),
    suggestMessage: vi.fn(missing),
    dismissSuggestion: vi.fn(missing),
    commit: vi.fn(missing),
    outsideDiff: vi.fn(missing),
    restore: vi.fn(missing),
    commitDetail: vi.fn(missing),
    repoStatus: vi.fn(missing),
    ...overrides,
  }
}

describe('start notices', () => {
  it('names an unknown or unreachable start and a changed branch, nothing otherwise', () => {
    expect(startNotices(view())).toEqual([])
    expect(startNotices(view({ startState: 'unknown', startHead: undefined }))).toEqual([
      { kind: 'unknown' },
    ])
    expect(
      startNotices(view({ startState: 'unreachable', branchChanged: true, branch: undefined })),
    ).toEqual([
      { kind: 'unreachable', start: START },
      { kind: 'branchChanged', from: 'main', to: null },
    ])
    expect(startNotices(view({ repo: false, startState: 'unknown' }))).toEqual([])
  })

  it('says when the history could not be read, instead of calling the start unknown', () => {
    expect(
      startNotices(view({ startState: 'unknown', historyState: 'history_unavailable' })),
    ).toEqual([{ kind: 'historyUnavailable' }])
    expect(startNotices(view({ historyState: 'history_unavailable' }))).toEqual([
      { kind: 'historyUnavailable' },
    ])
  })

  it('renders each notice in words', async () => {
    const { plugins } = await setupApp()
    const w = mount(GitStartNotice, {
      props: {
        notices: [
          { kind: 'unreachable', start: START },
          { kind: 'branchChanged', from: 'main', to: null },
          { kind: 'historyUnavailable' },
        ],
      },
      global: { plugins },
    })
    const items = w.findAll('li')
    expect(items).toHaveLength(3)
    expect(items[2]?.text()).toContain('could not be read this time')
    expect(items[0]?.text()).toContain('aaaaaaaaaa')
    expect(items[0]?.text()).toContain('no longer in the history')
    expect(items[1]?.text()).toContain('started on main')
    expect(items[1]?.text()).toContain('a detached HEAD')
  })
})

describe('useGitHistory', () => {
  async function setup(api: GitApi, initial: TaskGit) {
    const { plugins } = await setupApp()
    const git = shallowRef<TaskGit | null>(initial)
    let model!: GitHistoryModel
    mount(Host, {
      props: {
        make: () => {
          model = useGitHistory(
            () => 't1',
            () => git.value,
            api,
          )
        },
      },
      global: { plugins },
    })
    await flush(4)
    return { git, model: () => model }
  }

  it('selects the newest commit, reads each commit once, and picks its first file', async () => {
    const newest = commit('b', 'second')
    const older = commit('d', 'first', { turn: 2 })
    const commitDetail = vi.fn((_id: string, sha: string) =>
      Promise.resolve(
        detailOf(sha === newest.sha ? newest : older, sha === newest.sha ? ['b.ts'] : ['d.ts']),
      ),
    )
    const api = fakeApi({ commitDetail })
    const { git, model } = await setup(api, view({ commits: [newest, older] }))
    const m = model()
    expect(m.hasHistory.value).toBe(true)
    expect(m.selectedCommit.value).toBe(newest.sha)
    expect(commitDetail).toHaveBeenCalledWith('t1', newest.sha, expect.any(AbortSignal))
    expect(m.commitDiff.value?.file.path).toBe('b.ts')

    m.selectedCommit.value = older.sha
    await flush(4)
    expect(m.commitDiff.value?.diff?.hunks[0]?.lines[0]?.text).toBe('in d.ts')
    m.selectedCommit.value = newest.sha
    await flush(4)
    expect(commitDetail).toHaveBeenCalledTimes(2) // the newest came from the cache

    // A new read of the view keeps the selection while the commit is still listed.
    git.value = view({ commits: [newest, older] })
    await flush(4)
    expect(m.selectedCommit.value).toBe(newest.sha)
    expect(commitDetail).toHaveBeenCalledTimes(2)
  })

  it("lists the task's own commits before the others and selects the newest own one", async () => {
    const other = commit('e', 'someone else', { turn: 0, outsideTurns: true })
    const mine = commit('b', 'mine')
    const api = fakeApi({ commitDetail: vi.fn(() => Promise.resolve(detailOf(mine, ['b.ts']))) })
    const { model } = await setup(
      api,
      view({ commits: [other, mine], commitsMore: 7, ownCommits: 2 }),
    )
    expect(model().commits.value.map((c) => c.subject)).toEqual(['mine', 'someone else'])
    // Own: 2 (one listed, one older); others: the 9 commits of the range less those.
    expect([model().own.value, model().others.value]).toEqual([2, 7])
    expect(model().selectedCommit.value).toBe(mine.sha)
  })

  it('diffs a committed file against the start, and only in the files mode', async () => {
    const api = fakeApi()
    const { model } = await setup(
      api,
      view({ commits: [commit('b', 's')], committedFiles: [file('z.ts'), file('a.ts')] }),
    )
    const m = model()
    expect(api.diff).not.toHaveBeenCalled()
    m.mode.value = 'files'
    await flush(4)
    expect(m.selectedFile.value).toBe('a.ts')
    expect(api.diff).toHaveBeenLastCalledWith(
      't1',
      'a.ts',
      undefined,
      expect.any(AbortSignal),
      'start',
    )
    expect(m.fileDiff.value.status).toBe('ready')
  })

  it('reports a commit that cannot be read, and reads it again on retry', async () => {
    const c = commit('b', 's')
    const commitDetail = vi
      .fn<GitApi['commitDetail']>()
      .mockRejectedValueOnce(new Error('boom'))
      .mockResolvedValueOnce(detailOf(c, ['b.ts']))
    const { model } = await setup(fakeApi({ commitDetail }), view({ commits: [c] }))
    expect(model().commit.value).toEqual({ status: 'error', sha: c.sha, error: 'boom' })
    await model().reloadCommit()
    expect(model().commit.value.status).toBe('ready')
  })
})

describe('repository status', () => {
  const repo = (extra: Partial<Extract<RepoStatus, { repo: true }>> = {}): RepoStatus => ({
    repo: true,
    root: 'D:/repo',
    branch: 'main',
    detached: false,
    dirty: true,
    files: [file('mine.ts'), file('old.ts'), file('z/other.ts'), file('done.ts')],
    ...extra,
  })

  it("marks the repository's other files and leaves out the task's own", () => {
    const git = view({
      files: [file('mine.ts')],
      committedFiles: [file('done.ts')],
      preexisting: ['old.ts'],
    })
    expect(otherFiles(repo(), git).map((o) => [o.file.path, o.mark])).toEqual([
      ['old.ts', 'preexisting'],
      ['z/other.ts', 'untouched'],
    ])
    expect(otherFiles({ repo: false }, git)).toEqual([])
  })

  it("leaves out the files of the task's untracked folders", () => {
    const git = view({
      untrackedDirs: [{ path: 'z/', count: 25, bytes: 1, files: [] }],
      preexisting: ['old.ts'],
    })
    expect(otherFiles(repo(), git).map((o) => o.file.path)).toEqual([
      'done.ts',
      'mine.ts',
      'old.ts',
    ])
  })

  it('reads the upstream, ahead and behind', () => {
    expect(upstreamOf(repo())).toBeNull()
    expect(upstreamOf(repo({ upstream: 'origin/main', ahead: 2 }))).toEqual({
      name: 'origin/main',
      ahead: 2,
      behind: 0,
      gone: false,
    })
  })

  it('reads the repository again with every read of the task view', async () => {
    const { plugins } = await setupApp()
    const repoStatus = vi.fn(() => Promise.resolve(repo({ upstream: 'origin/main', behind: 1 })))
    const git = shallowRef<TaskGit | null>(view())
    let model!: RepoStatusModel
    mount(Host, {
      props: {
        make: () => {
          model = useRepoStatus(() => git.value, fakeApi({ repoStatus }))
        },
      },
      global: { plugins },
    })
    await flush(4)
    expect(repoStatus).toHaveBeenCalledWith('D:/repo', expect.any(AbortSignal))
    expect(model.upstream.value?.behind).toBe(1)
    git.value = view()
    await flush(4)
    expect(repoStatus).toHaveBeenCalledTimes(2)
    git.value = view({ repo: false })
    await flush(4)
    expect(model.state.value).toEqual({ status: 'idle' })
  })

  it('lists the other files with their marks, and says the upstream in words', async () => {
    const { plugins } = await setupApp()
    const panel = mount(GitRepoPanel, {
      props: {
        state: { status: 'ready', repo: repo() },
        others: [
          { file: file('old.ts'), mark: 'preexisting' },
          { file: file('z/other.ts', { status: 'untracked' }), mark: 'untouched' },
        ],
        preexisting: ['old.ts'],
      },
      global: { plugins },
    })
    expect(panel.find('summary').text()).toContain('2 other changed files')
    expect(panel.findAll('.repo__row').map((r) => r.text())).toEqual([
      'MModifiedold.tsChanged before this task',
      'UNew, not trackedz/other.tsNot from this task',
    ])

    const up = mount(GitUpstream, {
      props: { upstream: { name: 'origin/main', ahead: 2, behind: 1, gone: false } },
      global: { plugins },
    })
    expect(up.find('.sr-only').text()).toBe(
      'Compared with origin/main: 2 commits ahead, 1 commit behind',
    )
    await up.setProps({ upstream: { name: 'origin/main', ahead: 0, behind: 0, gone: true } })
    expect(up.find('.sr-only').text()).toBe('origin/main no longer exists')
    // Gone is said in words on screen too, in the muted tone (no attention colour).
    expect(up.find('.up__gone').text()).toBe('(gone)')
    expect(up.find('.up__short').text()).toBe('origin/main(gone)')
    expect(up.classes()).not.toContain('up--gone')
    await up.setProps({ upstream: null })
    expect(up.html()).toBe('<!--v-if-->')
  })
})

describe('commit list and history section', () => {
  it('moves through the commits with the arrow keys, Home and End', async () => {
    const { plugins } = await setupApp()
    const commits = [commit('1', 'one'), commit('2', 'two'), commit('3', 'three')]
    const selected = shallowRef<string | null>(commits[0]?.sha ?? null)
    const w = mount(GitCommitList, {
      props: {
        commits,
        more: 4,
        othersTotal: 0,
        label: 'Commits',
        selected: selected.value,
        'onUpdate:selected': (v: string | null) => {
          selected.value = v
          void w.setProps({ selected: v })
        },
      },
      global: { plugins },
      attachTo: document.body,
    })
    const list = w.find('[role="listbox"]').element
    press('ArrowDown', {}, list)
    await flush()
    expect(selected.value).toBe(commits[1]?.sha)
    press('End', {}, list)
    await flush()
    expect(selected.value).toBe(commits[2]?.sha)
    press('Home', {}, list)
    await flush()
    expect(selected.value).toBe(commits[0]?.sha)
    expect(w.find('.gcl__more').text()).toBe('4 older commits not shown')
    expect(w.findAll('[role="option"]')[0]?.text()).toContain('Turn 1')
    w.unmount()
  })

  it('shows the commits and their diff, and the files with their change since the start', async () => {
    const { plugins } = await setupApp()
    const c = commit('b', 'Add the parser', { fileCount: 1 })
    const api = fakeApi({
      commitDetail: vi.fn(() => Promise.resolve(detailOf(c, ['parser.go']))),
    })
    const w = mount(Host, {
      props: {
        make: () => {
          const history = useGitHistory(
            () => 't1',
            () =>
              view({
                commits: [c, commit('e', 'theirs', { turn: 0, outsideTurns: true })],
                commitsMore: 2,
                ownCommits: 3,
                committedFiles: [file('parser.go')],
              }),
            api,
          )
          return () => h(GitHistory, { history, wrap: false })
        },
      },
      global: { plugins },
    })
    await flush(6)
    expect(w.find('h3').text()).toBe('Committed in this task')
    expect(w.find('.gcd__subject').text()).toBe('Add the parser')
    expect(w.find('.gcd__body').text()).toBe('Why it changed.')
    expect(w.text()).toContain('in parser.go')
    // The count is the task's own commits, the older ones the list leaves out included; the others have theirs.
    expect(w.findAll('[role="radio"]').map((r) => r.text())).toEqual(['3 commits', '1 file'])
    expect(w.find('[data-group="others"] .gcl__title').text()).toBe(
      '1 other commit since the task started',
    )

    await w.findAll('[role="radio"]')[1]?.trigger('click')
    await flush(6)
    expect(w.text()).toContain('Whole change since the task started')
    expect(w.text()).toContain('other commits to this file since then are included')
    expect(w.text()).toContain('since start')
  })

  it('groups the commits made outside the task turns apart, muted, and moves through both', async () => {
    const { plugins } = await setupApp()
    const own = [commit('1', 'one'), commit('2', 'two', { turn: 2 })]
    const others = [commit('8', 'theirs', { turn: 0, outsideTurns: true })]
    const selected = shallowRef<string | null>(own[0]?.sha ?? null)
    const w = mount(GitCommitList, {
      props: {
        commits: [...own, ...others],
        more: 0,
        othersTotal: 4,
        label: 'Commits',
        selected: selected.value,
        'onUpdate:selected': (v: string | null) => {
          selected.value = v
          void w.setProps({ selected: v })
        },
      },
      global: { plugins },
      attachTo: document.body,
    })
    const groups = w.findAll('[role="group"]')
    expect(groups.map((g) => g.attributes('data-group'))).toEqual(['own', 'others'])
    expect(groups[0]?.find('.gcl__title').text()).toBe("Made during this task's turns")
    expect(groups[1]?.find('.gcl__title').text()).toBe('4 other commits since the task started')
    expect(groups[1]?.find('.gcl__hint').text()).toContain('not counted as this task')
    const labelled = groups[1]?.attributes('aria-labelledby') ?? ''
    expect(w.find(`#${CSS.escape(labelled)}`).text()).toBe('4 other commits since the task started')
    expect(groups[1]?.findAll('[role="option"]').map((o) => o.classes())).toEqual([
      expect.arrayContaining(['gcl__row--other']),
    ])

    const list = w.find('[role="listbox"]').element
    press('ArrowDown', {}, list)
    await flush()
    press('ArrowDown', {}, list)
    await flush()
    expect(selected.value).toBe(others[0]?.sha)
    expect(document.activeElement?.textContent).toContain('theirs')
    press('Home', {}, list)
    await flush()
    expect(selected.value).toBe(own[0]?.sha)
    w.unmount()

    // With only the task's own commits there are no group headings.
    const plain = mount(GitCommitList, {
      props: { commits: own, more: 0, othersTotal: 0, label: 'Commits', selected: null },
      global: { plugins },
    })
    expect(plain.findAll('[role="group"]')).toHaveLength(0)
    expect(plain.find('.gcl__title').exists()).toBe(false)
  })

  it("marks a commit made outside the task's turns in its detail", async () => {
    const { plugins } = await setupApp()
    const c = commit('e', 'theirs', { turn: 0, outsideTurns: true })
    const api = fakeApi({ commitDetail: vi.fn(() => Promise.resolve(detailOf(c, ['x.go']))) })
    const w = mount(Host, {
      props: {
        make: () => {
          const history = useGitHistory(
            () => 't1',
            () => view({ commits: [c] }),
            api,
          )
          return () => h(GitHistory, { history, wrap: false })
        },
      },
      global: { plugins },
    })
    await flush(6)
    expect(w.find('.gcd__outside').text()).toBe("Outside the task's turns")
    expect(w.find('[data-group="others"]').exists()).toBe(true)
  })
})
