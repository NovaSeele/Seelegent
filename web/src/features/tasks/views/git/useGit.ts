/**
 * State of the Git view of one task: the task's git status (re-read when the task's files, status or turn
 * change, and on every update while a suggested message is being written: the contract fills it in and says so
 * through `task.updated`, so nothing polls), the selected file's diff, and the writes (suggest, dismiss, commit).
 *
 * A failed read is an error with its message and a retry, never "not a repository" (old B11): that state comes
 * only from `repo: false`. A failed refresh keeps the last good data and shows the error beside it.
 */
import { computed, onBeforeUnmount, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { gitApi, isAbortError, type CommitResult, type GitApi } from '@/api'
import type { FileDiff, TaskGit, TaskSummary } from '@/api/types'
import { useToastStore } from '@/stores/toast'

import { fileSignature, sortFiles } from './git'

export type GitState =
  | { status: 'loading' }
  | { status: 'error'; error: string }
  | { status: 'ready'; git: TaskGit; refreshError: string | null }

export type DiffState =
  | { status: 'idle' }
  | { status: 'loading'; path: string }
  | { status: 'ready'; path: string; diff: FileDiff }
  | { status: 'error'; path: string; error: string }

export type GitBusy = 'suggest' | 'dismiss' | 'commit' | null

const messageOf = (error: unknown) => (error instanceof Error ? error.message : String(error))

export function useGit(task: () => TaskSummary, api: GitApi = gitApi) {
  const { t } = useI18n()
  const toast = useToastStore()
  const id = () => task().id

  const state = shallowRef<GitState>({ status: 'loading' })
  const git = computed(() => (state.value.status === 'ready' ? state.value.git : null))
  const files = computed(() => sortFiles(git.value?.files ?? []))
  // Large untracked folders are one row each; an open one lists its first files, which select and commit like
  // the others (`shown`). A folder is never committed as a whole.
  const dirs = computed(() => git.value?.untrackedDirs ?? [])
  const expanded = shallowRef<ReadonlySet<string>>(new Set())
  const dirFiles = computed(() => dirs.value.flatMap((d) => d.files))
  const shown = computed(() => [
    ...files.value,
    ...dirs.value.filter((d) => expanded.value.has(d.path)).flatMap((d) => d.files),
  ])
  /** Files of the task left out of `shown`: in closed folders, or past an open folder's list. */
  const hidden = computed(
    () => dirs.value.reduce((n, d) => n + d.count, 0) - (shown.value.length - files.value.length),
  )
  const suggestion = computed(() => git.value?.suggestion ?? null)

  let statusCall: AbortController | null = null
  async function load() {
    statusCall?.abort()
    const call = (statusCall = new AbortController())
    if (state.value.status === 'error') state.value = { status: 'loading' }
    try {
      const next = await api.status(id(), call.signal)
      if (call === statusCall) apply(next)
    } catch (error) {
      if (isAbortError(error) || call !== statusCall) return
      const current = state.value
      state.value =
        current.status === 'ready'
          ? { ...current, refreshError: messageOf(error) }
          : { status: 'error', error: messageOf(error) }
    }
  }

  function apply(next: TaskGit) {
    state.value = { status: 'ready', git: next, refreshError: null }
  }

  watch(() => `${id()}|${task().filesChanged}|${task().status}|${task().turnCount}`, load, {
    immediate: true,
  })
  watch(
    () => task().updatedAt,
    () => {
      if (suggestion.value?.state === 'running') void load()
    },
  )

  // The selected file and its diff, re-read when the file itself changes.
  const selected = shallowRef<string | null>(null)
  const selectedFile = computed(
    () => [...files.value, ...dirFiles.value].find((f) => f.path === selected.value) ?? null,
  )
  const diff = shallowRef<DiffState>({ status: 'idle' })

  watch([files, dirFiles], ([list, more]) => {
    if (
      !list.some((f) => f.path === selected.value) &&
      !more.some((f) => f.path === selected.value)
    ) {
      selected.value = list[0]?.path ?? null
    }
  })

  let diffCall: AbortController | null = null
  async function loadDiff() {
    diffCall?.abort()
    const file = selectedFile.value
    if (!file) {
      diff.value = { status: 'idle' }
      return
    }
    const call = (diffCall = new AbortController())
    const path = file.path
    diff.value = { status: 'loading', path }
    try {
      // The uncommitted list diffs against HEAD: what a commit of this file would take.
      const result = await api.diff(id(), path, undefined, call.signal, 'head')
      if (call === diffCall) diff.value = { status: 'ready', path, diff: result }
    } catch (error) {
      if (isAbortError(error) || call !== diffCall) return
      diff.value = { status: 'error', path, error: messageOf(error) }
    }
  }
  watch(
    () => (selectedFile.value ? fileSignature(selectedFile.value) + selected.value : ''),
    loadDiff,
  )

  onBeforeUnmount(() => {
    statusCall?.abort()
    diffCall?.abort()
  })

  const busy = shallowRef<GitBusy>(null)

  async function write(name: Exclude<GitBusy, null>, call: () => Promise<TaskGit>) {
    if (busy.value) return
    busy.value = name
    try {
      apply(await call())
    } catch (error) {
      toast.show({ message: t('git.failed', { error: messageOf(error) }), tone: 'error' })
    } finally {
      busy.value = null
    }
  }

  /**
   * Commits exactly `paths`. Resolves to the result, or to the daemon's message, which the commit dialog shows
   * in place (no toast, so a failure is reported once).
   */
  async function commit(
    message: string,
    paths: readonly string[],
  ): Promise<{ ok: true; result: CommitResult } | { ok: false; error: string }> {
    if (busy.value) return { ok: false, error: t('git.commit.busy') }
    busy.value = 'commit'
    try {
      const result = await api.commit(id(), message, paths)
      toast.show({
        message: t('git.commit.done', { sha: result.sha, summary: result.summary }),
        tone: 'success',
      })
      void load()
      return { ok: true, result }
    } catch (error) {
      return { ok: false, error: messageOf(error) }
    } finally {
      busy.value = null
    }
  }

  return {
    state,
    git,
    files,
    dirs,
    expanded,
    shown,
    hidden,
    suggestion,
    selected,
    selectedFile,
    diff,
    busy,
    reload: load,
    reloadDiff: loadDiff,
    suggest: () => write('suggest', () => api.suggestMessage(id())),
    dismiss: () => write('dismiss', () => api.dismissSuggestion(id())),
    commit,
  }
}

export type GitModel = ReturnType<typeof useGit>
