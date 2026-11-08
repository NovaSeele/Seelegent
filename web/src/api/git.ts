/** Typed endpoints of a task's git view (contract §3.3). Diffs arrive parsed; the UI never parses diff text. */
import type { ApiClient } from './client'
import type {
  CommitResult,
  FileDiff,
  GitCommitDetail,
  RepoStatus,
  RestoreResult,
  TaskGit,
} from './types'

export type { CommitResult }

/** What a task diff compares the working tree with: the task's start (the default) or HEAD. */
export type DiffBase = 'start' | 'head'

const enc = encodeURIComponent

export function createGitApi(client: ApiClient) {
  return {
    status: (taskId: string, signal?: AbortSignal) =>
      client.get<TaskGit>(`/tasks/${enc(taskId)}/git`, { signal }),

    /**
     * One file in the working tree against the task's start (`base` start, the default: committed and uncommitted
     * work together) or against HEAD (`base` head: what a commit would take). Without a usable start both are HEAD.
     */
    diff: (taskId: string, path: string, turn?: number, signal?: AbortSignal, base?: DiffBase) =>
      client.get<FileDiff>(`/tasks/${enc(taskId)}/git/diff`, {
        signal,
        query: { path, turn, base },
      }),

    /** One of the task's commits (start..HEAD) with its files and parsed diffs; 404 for any other commit. */
    commitDetail: (taskId: string, sha: string, signal?: AbortSignal) =>
      client.get<GitCommitDetail>(`/tasks/${enc(taskId)}/git/commits/${enc(sha)}`, { signal }),

    /** The whole repository around `cwd`: every uncommitted file and the branch's upstream, ahead and behind. */
    repoStatus: (cwd: string, signal?: AbortSignal) =>
      client.get<RepoStatus>('/git/status', { signal, query: { cwd } }),

    /** 202: the suggestion fills in later; the task's next task.updated says to read the git view again. */
    suggestMessage: (taskId: string) =>
      client.post<TaskGit>(`/tasks/${enc(taskId)}/git/suggest-message`, { body: {} }),

    dismissSuggestion: (taskId: string) =>
      client.post<TaskGit>(`/tasks/${enc(taskId)}/git/suggest-message/dismiss`, { body: {} }),

    /** Stages exactly `files` and commits them; 409 bad_state while the task is active. */
    commit: (taskId: string, message: string, files: readonly string[]) =>
      client.post<CommitResult>(`/tasks/${enc(taskId)}/git/commit`, {
        body: { message, files: [...files] },
      }),

    /** What changed in the turn outside the worker's edit tools; 404 for a turn without snapshots. */
    outsideDiff: (taskId: string, turn: number, signal?: AbortSignal) =>
      client.get<FileDiff[]>(`/tasks/${enc(taskId)}/turns/${turn}/outside-diff`, { signal }),

    /**
     * Puts work-at-risk files back from the turn's start snapshot (all of them without `files`). Files changed
     * after the turn ended are skipped (code changed_since_turn) unless `force`.
     */
    restore: (
      taskId: string,
      turn: number,
      opts: { files?: readonly string[]; force?: boolean } = {},
    ) =>
      client.post<RestoreResult>(`/tasks/${enc(taskId)}/turns/${turn}/restore`, {
        body: {
          ...(opts.files?.length ? { files: [...opts.files] } : {}),
          ...(opts.force ? { force: true } : {}),
        },
      }),
  }
}

export type GitApi = ReturnType<typeof createGitApi>
