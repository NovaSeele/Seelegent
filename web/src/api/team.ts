/** Typed endpoints of the team: members, consults, review notes, meetings and contests (contract §5, §6). */
import type { ApiClient } from './client'
import type {
  Consult,
  ConsultRequest,
  Contest,
  ContestDiff,
  ContestPickRequest,
  ContestSummary,
  DissentOutcomeRequest,
  Meeting,
  MeetingActRequest,
  MeetingActResult,
  MeetingDecideRequest,
  MeetingPostRequest,
  MeetingRequest,
  MeetingSummary,
  Member,
  MemberInput,
  MemberTrack,
  Note,
  NoteInput,
  NotePatch,
  NotesSendRequest,
  NotesSendResult,
  RunEstimate,
  Tier,
} from './types'

const seg = encodeURIComponent

/**
 * What a member save sends: MemberInput, where an empty tier clears the tier (the daemon keeps tiers as plain
 * strings and treats '' as none; the generated union has no empty member).
 */
export type MemberWrite = Omit<MemberInput, 'tier'> & { tier?: Tier | '' }

export function createTeamApi(client: ApiClient) {
  return {
    members: (signal?: AbortSignal) => client.get<Member[]>('/members', { signal }),
    memberTrack: (signal?: AbortSignal) => client.get<MemberTrack[]>('/members/track', { signal }),
    createMember: (body: MemberWrite) => client.post<Member>('/members', { body }),
    /** A merge onto the stored member: only the fields sent change; the id cannot. */
    patchMember: (id: string, body: Partial<MemberWrite>) =>
      client.patch<Member>(`/members/${seg(id)}`, { body }),
    deleteMember: (id: string) => client.delete<void>(`/members/${seg(id)}`),

    consult: (taskId: string, body: ConsultRequest) =>
      client.post<Consult>(`/tasks/${seg(taskId)}/consult`, { body }),

    notes: (taskId: string, signal?: AbortSignal) =>
      client.get<Note[]>(`/tasks/${seg(taskId)}/notes`, { signal }),
    addNote: (taskId: string, body: NoteInput) =>
      client.post<Note>(`/tasks/${seg(taskId)}/notes`, { body }),
    patchNote: (taskId: string, noteId: string, body: NotePatch) =>
      client.patch<Note>(`/tasks/${seg(taskId)}/notes/${seg(noteId)}`, { body }),
    deleteNote: (taskId: string, noteId: string) =>
      client.delete<void>(`/tasks/${seg(taskId)}/notes/${seg(noteId)}`),
    sendNotes: (taskId: string, body: NotesSendRequest) =>
      client.post<NotesSendResult>(`/tasks/${seg(taskId)}/notes/send`, { body }),

    meetings: (signal?: AbortSignal) => client.get<MeetingSummary[]>('/meetings', { signal }),
    meeting: (id: string, signal?: AbortSignal) =>
      client.get<Meeting>(`/meetings/${seg(id)}`, { signal }),
    estimateMeeting: (body: MeetingRequest) =>
      client.post<RunEstimate>('/meetings/estimate', { body }),
    openMeeting: (body: MeetingRequest) => client.post<Meeting>('/meetings', { body }),
    postMeeting: (id: string, body: MeetingPostRequest) =>
      client.post<Meeting>(`/meetings/${seg(id)}/post`, { body }),
    decideMeeting: (id: string, body: MeetingDecideRequest) =>
      client.post<Meeting>(`/meetings/${seg(id)}/decide`, { body }),
    actMeeting: (id: string, body: MeetingActRequest) =>
      client.post<MeetingActResult>(`/meetings/${seg(id)}/act`, { body }),
    cancelMeeting: (id: string) =>
      client.post<Meeting>(`/meetings/${seg(id)}/cancel`, { body: {} }),
    dissentOutcome: (id: string, dissentId: string, outcome: DissentOutcomeRequest['outcome']) =>
      client.post<Meeting>(`/meetings/${seg(id)}/dissents/${seg(dissentId)}/outcome`, {
        body: { outcome },
      }),

    contests: (signal?: AbortSignal) => client.get<ContestSummary[]>('/contests', { signal }),
    contest: (id: string, signal?: AbortSignal) =>
      client.get<Contest>(`/contests/${seg(id)}`, { signal }),
    contestDiff: (id: string, label: string, signal?: AbortSignal) =>
      client.get<ContestDiff>(`/contests/${seg(id)}/variants/${seg(label)}/diff`, { signal }),
    pickContest: (id: string, body: ContestPickRequest) =>
      client.post<Contest>(`/contests/${seg(id)}/pick`, { body }),
    cancelContest: (id: string) =>
      client.post<Contest>(`/contests/${seg(id)}/cancel`, { body: {} }),
  }
}

export type TeamApi = ReturnType<typeof createTeamApi>
