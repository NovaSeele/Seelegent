/**
 * The team: department members with their track records, and the meeting and contest lists. Each list is read
 * when a page first needs it, again on its change frame (members.changed, meeting.updated, contest.updated) and
 * after a resync. A meeting.updated or contest.updated frame carries the new summary, so the list is patched in
 * place; the open meeting or contest page reads its full record itself.
 */
import { defineStore } from 'pinia'
import { shallowRef } from 'vue'

import { teamApi as defaultApi } from '@/api'
import type { MemberWrite, TeamApi } from '@/api/team'
import type { ContestSummary, MeetingSummary, Member, MemberTrack } from '@/api/types'

import { useConnectionStore } from './connection'
import { createLoader } from './loader'

/** Puts `item` into `list` by id (newest first for a new one). */
export function upsertById<T extends { id: string }>(list: readonly T[], item: T): T[] {
  const at = list.findIndex((x) => x.id === item.id)
  if (at < 0) return [item, ...list]
  return list.map((x, i) => (i === at ? item : x))
}

const MEETING_ENDED: ReadonlySet<MeetingSummary['status']> = new Set([
  'closed',
  'cancelled',
  'failed',
])

export const useTeamStore = defineStore('team', () => {
  let client: TeamApi = defaultApi
  const members = shallowRef<readonly Member[]>([])
  const track = shallowRef<Readonly<Record<string, MemberTrack>>>({})
  const meetings = shallowRef<readonly MeetingSummary[]>([])
  const contests = shallowRef<readonly ContestSummary[]>([])

  const membersLoader = createLoader(
    async (signal) => {
      const [list, records] = await Promise.all([
        client.members(signal),
        client.memberTrack(signal).catch(() => [] as MemberTrack[]),
      ])
      return { list, records }
    },
    ({ list, records }) => {
      members.value = list
      track.value = Object.fromEntries(records.map((r) => [r.member, r]))
    },
  )
  const meetingsLoader = createLoader(
    (signal) => client.meetings(signal),
    (list) => (meetings.value = list),
  )
  const contestsLoader = createLoader(
    (signal) => client.contests(signal),
    (list) => (contests.value = list),
  )
  const watching = new Set<string>()

  function setApi(next: TeamApi): void {
    client = next
  }

  function api(): TeamApi {
    return client
  }

  function ensureMembers(): void {
    if (!watching.has('members')) {
      watching.add('members')
      const connection = useConnectionStore()
      connection.subscribe('members.changed', () => void membersLoader.load())
      // A meeting that ends changes the track records; its progress frames do not.
      connection.subscribe('meeting.updated', (m) => {
        if (!MEETING_ENDED.has(m.status)) return
        if (membersLoader.status.value !== 'idle') void membersLoader.load()
      })
      connection.onResync(() => void membersLoader.load())
    }
    if (membersLoader.status.value === 'idle') void membersLoader.load()
  }

  function ensureMeetings(): void {
    if (!watching.has('meetings')) {
      watching.add('meetings')
      const connection = useConnectionStore()
      connection.subscribe(
        'meeting.updated',
        (m) => (meetings.value = upsertById(meetings.value, m)),
      )
      connection.onResync(() => void meetingsLoader.load())
    }
    if (meetingsLoader.status.value === 'idle') void meetingsLoader.load()
  }

  function ensureContests(): void {
    if (!watching.has('contests')) {
      watching.add('contests')
      const connection = useConnectionStore()
      connection.subscribe(
        'contest.updated',
        (c) => (contests.value = upsertById(contests.value, c)),
      )
      connection.onResync(() => void contestsLoader.load())
    }
    if (contestsLoader.status.value === 'idle') void contestsLoader.load()
  }

  async function createMember(input: MemberWrite): Promise<Member> {
    const m = await client.createMember(input)
    members.value = [...members.value.filter((x) => x.id !== m.id), m]
    return m
  }

  async function patchMember(id: string, input: Partial<MemberWrite>): Promise<Member> {
    const m = await client.patchMember(id, input)
    members.value = members.value.map((x) => (x.id === id ? m : x))
    return m
  }

  async function removeMember(id: string): Promise<void> {
    await client.deleteMember(id)
    members.value = members.value.filter((x) => x.id !== id)
  }

  return {
    members,
    track,
    meetings,
    contests,
    membersStatus: membersLoader.status,
    membersError: membersLoader.error,
    meetingsStatus: meetingsLoader.status,
    meetingsError: meetingsLoader.error,
    contestsStatus: contestsLoader.status,
    contestsError: contestsLoader.error,
    loadMembers: membersLoader.load,
    loadMeetings: meetingsLoader.load,
    loadContests: contestsLoader.load,
    setApi,
    api,
    ensureMembers,
    ensureMeetings,
    ensureContests,
    createMember,
    patchMember,
    removeMember,
  }
})
