/**
 * Typed endpoints of the GitHub flow and the automations (milestone 15; docs/github.md, docs/automations.md).
 *
 * The token routes and the automation confirmation are dashboard-only: the daemon checks the page's Origin, the
 * browser's Sec-Fetch-* headers and `X-Seelegent-UI: 1`, which only these calls send. The token itself goes out
 * once on save and never comes back; the daemon answers its last four characters (tokenHint).
 */
import type { ApiClient } from './client'
import type {
  Automation,
  AutomationConfirmToken,
  AutomationEnableResult,
  AutomationInput,
  AutomationPatch,
  AutomationRun,
  CommentRequest,
  DropRequest,
  FixChecksRequest,
  GitHubAccess,
  GitHubTokenResult,
  Issue,
  IssueInput,
  LinkPRRequest,
  PRRequest,
  RemoteRequest,
  Repo,
  RepoConnectRequest,
  RepoPatch,
  SchedulePreview,
  SchedulePreviewRequest,
  SuggestRequest,
  SyncResult,
  Ticket,
  TicketSummary,
  TicketTaskRequest,
  TicketTaskResult,
  TriageRequest,
  TriageResult,
} from './types'

const seg = encodeURIComponent

/** The header that marks a request as the dashboard's own (with the browser's Origin and Sec-Fetch-*). */
export const DASHBOARD_HEADERS: Readonly<Record<string, string>> = { 'X-Seelegent-UI': '1' }

export type IssueState = 'open' | 'closed' | 'all'

export function createGitHubApi(client: ApiClient) {
  const repo = (id: string, rest = '') => `/github/repos/${seg(id)}${rest}`
  const ticket = (id: string, rest = '') => `/github/tickets/${seg(id)}${rest}`
  return {
    access: (refresh = false, signal?: AbortSignal) =>
      client.get<GitHubAccess>('/github/access', {
        query: { refresh: refresh ? 1 : undefined },
        signal,
      }),
    saveToken: (token: string) =>
      client.put<GitHubTokenResult>('/github/token', {
        body: { token },
        headers: { ...DASHBOARD_HEADERS },
      }),
    removeToken: () => client.delete<void>('/github/token', { headers: { ...DASHBOARD_HEADERS } }),
    poll: () => client.post<void>('/github/poll', { body: {} }),

    repos: (signal?: AbortSignal) => client.get<Repo[]>('/github/repos', { signal }),
    connectRepo: (body: RepoConnectRequest) => client.post<Repo>('/github/repos', { body }),
    patchRepo: (id: string, body: RepoPatch) => client.patch<Repo>(repo(id), { body }),
    disconnectRepo: (id: string) => client.delete<void>(repo(id)),
    createRemote: (id: string, body: RemoteRequest) =>
      client.post<Repo>(repo(id, '/remote'), { body }),
    syncRepo: (id: string) => client.post<SyncResult>(repo(id, '/sync'), { body: {} }),

    issues: (id: string, state: IssueState = 'open', signal?: AbortSignal) =>
      client.get<Issue[]>(repo(id, '/issues'), { query: { state }, signal }),
    addIssue: (id: string, body: IssueInput) => client.post<Issue>(repo(id, '/issues'), { body }),
    suggest: (id: string, key: string, body: SuggestRequest) =>
      client.post<Issue>(repo(id, `/issues/${seg(key)}/suggest`), { body }),
    triage: (id: string, key: string, body: TriageRequest) =>
      client.post<TriageResult>(repo(id, `/issues/${seg(key)}/triage`), { body }),

    tickets: (repoId?: string, signal?: AbortSignal) =>
      client.get<TicketSummary[]>('/github/tickets', { query: { repo: repoId }, signal }),
    ticket: (id: string, signal?: AbortSignal) => client.get<Ticket>(ticket(id), { signal }),
    addTask: (id: string, body: TicketTaskRequest) =>
      client.post<TicketTaskResult>(ticket(id, '/tasks'), { body }),
    retry: (id: string, body: TicketTaskRequest) =>
      client.post<TicketTaskResult>(ticket(id, '/retry'), { body }),
    openPR: (id: string, body: PRRequest) => client.post<Ticket>(ticket(id, '/pr'), { body }),
    linkPR: (id: string, body: LinkPRRequest) =>
      client.post<Ticket>(ticket(id, '/link-pr'), { body }),
    comment: (id: string, body: CommentRequest) =>
      client.post<Ticket>(ticket(id, '/comment'), { body }),
    drop: (id: string, body: DropRequest) => client.post<Ticket>(ticket(id, '/drop'), { body }),
    cleanup: (id: string) => client.post<Ticket>(ticket(id, '/cleanup'), { body: {} }),
    fixChecks: (id: string, body: FixChecksRequest) =>
      client.post<TicketTaskResult>(ticket(id, '/fix-checks'), { body }),
  }
}

export type GitHubApi = ReturnType<typeof createGitHubApi>

export function createAutomationsApi(client: ApiClient) {
  const one = (id: string, rest = '') => `/automations/${seg(id)}${rest}`
  return {
    list: (signal?: AbortSignal) => client.get<Automation[]>('/automations', { signal }),
    get: (id: string, signal?: AbortSignal) => client.get<Automation>(one(id), { signal }),
    /** Always created disabled; `enabled: true` only records the request (the answer is the enable result). */
    create: (body: AutomationInput) => client.post<Automation>('/automations', { body }),
    patch: (id: string, body: AutomationPatch) => client.patch<Automation>(one(id), { body }),
    remove: (id: string) => client.delete<void>(one(id)),
    requestEnable: (id: string) =>
      client.post<AutomationEnableResult>(one(id, '/enable'), { body: {} }),
    /** `revision` is the Revision the page shows: the daemon refuses a token for content that changed since. */
    confirmToken: (id: string, revision: string) =>
      client.post<AutomationConfirmToken>(one(id, '/confirm-token'), {
        body: { revision },
        headers: { ...DASHBOARD_HEADERS },
      }),
    confirm: (id: string, token: string) =>
      client.post<Automation>(one(id, '/confirm'), {
        body: { token },
        headers: { ...DASHBOARD_HEADERS },
      }),
    run: (id: string) => client.post<AutomationRun>(one(id, '/run'), { body: {} }),
    runs: (id: string, limit = 50, signal?: AbortSignal) =>
      client.get<AutomationRun[]>(one(id, '/runs'), { query: { limit }, signal }),
    preview: (body: SchedulePreviewRequest, signal?: AbortSignal) =>
      client.post<SchedulePreview>('/automations/preview', { body, signal }),
  }
}

export type AutomationsApi = ReturnType<typeof createAutomationsApi>
