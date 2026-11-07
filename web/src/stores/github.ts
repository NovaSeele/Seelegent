/**
 * The GitHub flow (milestone 15): the access tier, the connected repos, the inbox of the chosen repo and the
 * tickets. Everything is read when the Issues page first needs it and again on its frames: issues.changed (a
 * repo, its inbox or the access changed; repoId "" means the access) and ticket.updated (the new summary, patched
 * into the list in place), and after a resync.
 */
import { defineStore } from 'pinia'
import { computed, shallowRef } from 'vue'

import { githubApi as defaultApi } from '@/api'
import type { GitHubApi, IssueState } from '@/api/github'
import type { GitHubAccess, Issue, Repo, TicketSummary } from '@/api/types'
import { readStored, writeStored, type StoredValue } from '@/lib/storage'

import { useConnectionStore } from './connection'
import { createLoader } from './loader'
import { upsertById } from './team'

const LAST_REPO: StoredValue<string> = {
  name: 'issues.repo',
  fallback: '',
  validate: (v): v is string => typeof v === 'string' && v.length < 64,
}

export const useGitHubStore = defineStore('github', () => {
  let client: GitHubApi = defaultApi
  const access = shallowRef<GitHubAccess | null>(null)
  const repos = shallowRef<readonly Repo[]>([])
  const tickets = shallowRef<readonly TicketSummary[]>([])
  const issues = shallowRef<readonly Issue[]>([])
  const repoId = shallowRef(readStored(LAST_REPO))
  const issueState = shallowRef<IssueState>('open')

  const repo = computed(() => repos.value.find((r) => r.id === repoId.value) ?? null)

  const accessLoader = createLoader(
    (signal) => client.access(false, signal),
    (a) => (access.value = a),
  )
  const reposLoader = createLoader(
    (signal) => client.repos(signal),
    (list) => {
      repos.value = list
      if (!list.some((r) => r.id === repoId.value)) select(list[0]?.id ?? '')
    },
  )
  const ticketsLoader = createLoader(
    (signal) => client.tickets(undefined, signal),
    (list) => (tickets.value = list),
  )
  const issuesLoader = createLoader(
    (signal) =>
      repoId.value
        ? client.issues(repoId.value, issueState.value, signal)
        : Promise.resolve([] as Issue[]),
    (list) => (issues.value = list),
  )
  let watching = false

  function setApi(next: GitHubApi): void {
    client = next
  }

  function api(): GitHubApi {
    return client
  }

  function select(id: string): void {
    if (id === repoId.value) return
    repoId.value = id
    writeStored(LAST_REPO, id)
    issues.value = []
    if (watching) void issuesLoader.load()
  }

  function setIssueState(state: IssueState): void {
    if (state === issueState.value) return
    issueState.value = state
    void issuesLoader.load()
  }

  function reloadAll(): void {
    void accessLoader.load()
    void reposLoader.load().then(() => issuesLoader.load())
    void ticketsLoader.load()
  }

  function ensure(): void {
    if (!watching) {
      watching = true
      const connection = useConnectionStore()
      connection.subscribe('issues.changed', (e) => {
        if (!e.repoId) void accessLoader.load()
        void reposLoader.load()
        if (!e.repoId || e.repoId === repoId.value) void issuesLoader.load()
      })
      connection.subscribe('ticket.updated', (t) => {
        tickets.value = upsertById(tickets.value, t)
        if (t.repoId === repoId.value) void issuesLoader.load()
      })
      connection.onResync(reloadAll)
      reloadAll()
      return
    }
    if (reposLoader.status.value === 'idle') reloadAll()
  }

  async function refreshAccess(): Promise<void> {
    access.value = await client.access(true)
  }

  function putRepo(r: Repo): void {
    repos.value = upsertById(repos.value, r)
  }

  async function connect(path: string, init: boolean): Promise<Repo> {
    const r = await client.connectRepo({ path, ...(init ? { init: true } : {}) })
    putRepo(r)
    select(r.id)
    return r
  }

  async function disconnect(id: string): Promise<void> {
    await client.disconnectRepo(id)
    repos.value = repos.value.filter((r) => r.id !== id)
    if (repoId.value === id) select(repos.value[0]?.id ?? '')
  }

  function putIssue(i: Issue): void {
    const at = issues.value.findIndex((x) => x.key === i.key)
    issues.value = at < 0 ? [i, ...issues.value] : issues.value.map((x, n) => (n === at ? i : x))
  }

  return {
    access,
    repos,
    repo,
    repoId,
    issues,
    issueState,
    tickets,
    accessStatus: accessLoader.status,
    reposStatus: reposLoader.status,
    reposError: reposLoader.error,
    issuesStatus: issuesLoader.status,
    issuesError: issuesLoader.error,
    issuesBusy: issuesLoader.busy,
    ticketsStatus: ticketsLoader.status,
    loadIssues: issuesLoader.load,
    loadRepos: reposLoader.load,
    loadTickets: ticketsLoader.load,
    setApi,
    api,
    ensure,
    select,
    setIssueState,
    refreshAccess,
    putRepo,
    putIssue,
    connect,
    disconnect,
  }
})
