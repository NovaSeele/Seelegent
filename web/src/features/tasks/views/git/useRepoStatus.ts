/**
 * The whole repository around a task (GET /git/status): the uncommitted files the task did not change, each
 * marked as changed before the task or simply not the task's, and the branch's upstream with the commits ahead
 * and behind it. It is read again every time the task's own git view is read, so both stay in step.
 */
import { computed, onBeforeUnmount, shallowRef, watch } from 'vue'

import { gitApi, isAbortError, type GitApi } from '@/api'
import type { GitFile, RepoStatus, TaskGit } from '@/api/types'

import { sortFiles } from './git'

export type RepoState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'ready'; repo: RepoStatus }
  | { status: 'error'; error: string }

/** An uncommitted file of the repository that is not among the task's files. */
export interface OtherFile {
  file: GitFile
  /** preexisting: it already had changes when the task started; untouched: changed, but not by the task. */
  mark: 'preexisting' | 'untouched'
}

export interface Upstream {
  name: string
  ahead: number
  behind: number
  gone: boolean
}

const messageOf = (error: unknown) => (error instanceof Error ? error.message : String(error))

/**
 * The repository's uncommitted files that are not the task's, with their marks. The files inside one of the
 * task's untracked folder rows are left out: that row stands for the whole folder and counts them (`others`).
 */
export function otherFiles(repo: RepoStatus | null, git: TaskGit | null): OtherFile[] {
  if (!repo?.repo || !git?.repo) return []
  const own = new Set([...git.files, ...git.committedFiles].map((f) => f.path))
  const ownDirs = (git.untrackedDirs ?? []).map((d) => d.path)
  const before = new Set(git.preexisting)
  return sortFiles(repo.files ?? [])
    .filter((f) => !own.has(f.path) && !ownDirs.some((dir) => f.path.startsWith(dir)))
    .map((file) => ({
      file,
      mark:
        before.has(file.path) || (file.oldPath !== undefined && before.has(file.oldPath))
          ? 'preexisting'
          : 'untouched',
    }))
}

export function upstreamOf(repo: RepoStatus | null): Upstream | null {
  if (!repo?.repo || !repo.upstream) return null
  return {
    name: repo.upstream,
    ahead: repo.ahead ?? 0,
    behind: repo.behind ?? 0,
    gone: repo.upstreamGone ?? false,
  }
}

export function useRepoStatus(git: () => TaskGit | null, api: GitApi = gitApi) {
  const state = shallowRef<RepoState>({ status: 'idle' })
  const repo = computed(() => (state.value.status === 'ready' ? state.value.repo : null))
  const others = computed(() => otherFiles(repo.value, git()))
  const upstream = computed(() => upstreamOf(repo.value))

  let call: AbortController | null = null
  async function load() {
    call?.abort()
    const root = git()?.repo ? git()?.root : undefined
    if (!root) {
      state.value = { status: 'idle' }
      return
    }
    const mine = (call = new AbortController())
    if (state.value.status !== 'ready') state.value = { status: 'loading' }
    try {
      const next = await api.repoStatus(root, mine.signal)
      if (mine === call) state.value = { status: 'ready', repo: next }
    } catch (error) {
      if (isAbortError(error) || mine !== call) return
      state.value = { status: 'error', error: messageOf(error) }
    }
  }

  // Every read of the task's view is a new object: read the repository with it.
  watch(git, load, { immediate: true })
  onBeforeUnmount(() => call?.abort())

  return { state, others, upstream, reload: load }
}

export type RepoStatusModel = ReturnType<typeof useRepoStatus>
