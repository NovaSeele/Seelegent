/**
 * State of what a task committed (the "Committed in this task" part of the Git view): the commits since the task
 * started and the files they changed, the selected file's diff against the start, and the selected commit with
 * its own files and diffs. A commit never changes, so its detail is read once and kept; a file's diff is read
 * again whenever the file or HEAD moves. Notices say when the view cannot tell the task's commits apart.
 */
import { computed, onBeforeUnmount, shallowRef, watch } from 'vue'

import { gitApi, isAbortError, type GitApi } from '@/api'
import type { GitCommitDetail, GitFile, TaskGit } from '@/api/types'

import { fileSignature, sortFiles } from './git'
import type { DiffState } from './useGit'

export type HistoryMode = 'commits' | 'files'

export type CommitState =
  | { status: 'idle' }
  | { status: 'loading'; sha: string }
  | { status: 'ready'; sha: string; detail: GitCommitDetail }
  | { status: 'error'; sha: string; error: string }

/** Why the view cannot (fully) follow the task's history; several can hold at once. */
export type StartNotice =
  | { kind: 'unknown' }
  | { kind: 'unreachable'; start: string }
  | { kind: 'branchChanged'; from: string; to: string | null }
  | { kind: 'historyUnavailable' }

const messageOf = (error: unknown) => (error instanceof Error ? error.message : String(error))

/** The notices for a task's git view, most serious first. */
export function startNotices(git: TaskGit | null): StartNotice[] {
  if (!git?.repo) return []
  const out: StartNotice[] = []
  if (git.startState === 'unreachable')
    out.push({ kind: 'unreachable', start: git.startHead ?? '' })
  // A history that could not be read says so; the start is then not known to be missing.
  if (git.historyState === 'history_unavailable') out.push({ kind: 'historyUnavailable' })
  else if (git.startState === 'unknown') out.push({ kind: 'unknown' })
  if (git.branchChanged && git.startBranch)
    out.push({ kind: 'branchChanged', from: git.startBranch, to: git.branch ?? null })
  return out
}

export function useGitHistory(
  taskId: () => string,
  git: () => TaskGit | null,
  api: GitApi = gitApi,
) {
  // The task's own commits first (made during its turns), then the others since its start; each newest first.
  const commits = computed(() => {
    const all = git()?.commits ?? []
    return [...all.filter((c) => !c.outsideTurns), ...all.filter((c) => c.outsideTurns)]
  })
  const files = computed(() => sortFiles(git()?.committedFiles ?? []))
  const more = computed(() => git()?.commitsMore ?? 0)
  // The task's own commits (made during its turns, listed or older) and the others since its start.
  const own = computed(() => git()?.ownCommits ?? 0)
  const others = computed(() => Math.max(0, commits.value.length + more.value - own.value))
  const notices = computed(() => startNotices(git()))
  const hasHistory = computed(() => commits.value.length > 0 || files.value.length > 0)
  const mode = shallowRef<HistoryMode>('commits')

  // Files mode: the file's whole change since the start.
  const selectedFile = shallowRef<string | null>(null)
  const file = computed<GitFile | null>(
    () => files.value.find((f) => f.path === selectedFile.value) ?? null,
  )
  const fileDiff = shallowRef<DiffState>({ status: 'idle' })
  watch(
    files,
    (list) => {
      if (!list.some((f) => f.path === selectedFile.value))
        selectedFile.value = list[0]?.path ?? null
    },
    { immediate: true },
  )

  let fileCall: AbortController | null = null
  async function loadFileDiff() {
    fileCall?.abort()
    const current = file.value
    if (!current || mode.value !== 'files') {
      if (!current) fileDiff.value = { status: 'idle' }
      return
    }
    const call = (fileCall = new AbortController())
    const path = current.path
    fileDiff.value = { status: 'loading', path }
    try {
      const diff = await api.diff(taskId(), path, undefined, call.signal, 'start')
      if (call === fileCall) fileDiff.value = { status: 'ready', path, diff }
    } catch (error) {
      if (isAbortError(error) || call !== fileCall) return
      fileDiff.value = { status: 'error', path, error: messageOf(error) }
    }
  }
  watch(
    () =>
      mode.value === 'files' && file.value
        ? `${fileSignature(file.value)}|${file.value.path}|${git()?.head ?? ''}`
        : '',
    loadFileDiff,
    { immediate: true },
  )

  // Commits mode: the selected commit, read once per sha.
  const selectedCommit = shallowRef<string | null>(null)
  const commit = shallowRef<CommitState>({ status: 'idle' })
  const commitFile = shallowRef<string | null>(null)
  const cache = new Map<string, GitCommitDetail>()
  watch(
    commits,
    (list) => {
      if (!list.some((c) => c.sha === selectedCommit.value))
        selectedCommit.value = list[0]?.sha ?? null
    },
    { immediate: true },
  )

  let commitCall: AbortController | null = null
  async function loadCommit() {
    commitCall?.abort()
    const sha = selectedCommit.value
    if (!sha || mode.value !== 'commits') {
      if (!sha) commit.value = { status: 'idle' }
      return
    }
    const kept = cache.get(sha)
    if (kept) {
      show(sha, kept)
      return
    }
    const call = (commitCall = new AbortController())
    commit.value = { status: 'loading', sha }
    try {
      const detail = await api.commitDetail(taskId(), sha, call.signal)
      if (call !== commitCall) return
      cache.set(sha, detail)
      show(sha, detail)
    } catch (error) {
      if (isAbortError(error) || call !== commitCall) return
      commit.value = { status: 'error', sha, error: messageOf(error) }
    }
  }
  function show(sha: string, detail: GitCommitDetail) {
    commit.value = { status: 'ready', sha, detail }
    if (!detail.files.some((f) => f.path === commitFile.value))
      commitFile.value = detail.files[0]?.path ?? null
  }
  watch(() => (mode.value === 'commits' ? (selectedCommit.value ?? '') : ''), loadCommit, {
    immediate: true,
  })

  /** The selected file of the selected commit and its diff (undefined: the diff was left out, over the limit). */
  const commitDiff = computed(() => {
    const state = commit.value
    if (state.status !== 'ready' || !commitFile.value) return null
    const f = state.detail.files.find((x) => x.path === commitFile.value)
    if (!f) return null
    return { file: f, diff: state.detail.diffs.find((d) => d.path === f.path) }
  })

  watch(taskId, () => cache.clear())
  onBeforeUnmount(() => {
    fileCall?.abort()
    commitCall?.abort()
  })

  return {
    commits,
    files,
    more,
    own,
    others,
    notices,
    hasHistory,
    mode,
    selectedFile,
    file,
    fileDiff,
    selectedCommit,
    commit,
    commitFile,
    commitDiff,
    reloadFileDiff: loadFileDiff,
    reloadCommit: loadCommit,
  }
}

export type GitHistoryModel = ReturnType<typeof useGitHistory>
