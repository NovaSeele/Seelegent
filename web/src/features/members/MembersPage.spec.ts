import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/api/client'
import type { TeamApi } from '@/api/team'
import type { Member, MemberTrack } from '@/api/types'
import { useTeamStore } from '@/stores/team'
import { setupApp } from '@/test/app'

import MembersPage from './MembersPage.vue'

const member: Member = {
  id: 'rev',
  name: 'Reviewer',
  backend: 'agy',
  rank: 'peer',
  preferredAccounts: [],
  fallbackBackends: [],
  enabled: true,
}

async function render(memberTrack: TeamApi['memberTrack']) {
  const { plugins } = await setupApp()
  const api = { members: vi.fn(() => Promise.resolve([member])), memberTrack }
  useTeamStore().setApi(api as unknown as TeamApi)
  const wrapper = mount(MembersPage, {
    global: { plugins, stubs: { MemberEditor: true, ConfirmDialog: true } },
  })
  await flushPromises()
  return { wrapper, api }
}

describe('MembersPage track records (E-B11)', () => {
  it('says the track records could not be read, never "none yet", and reads them again on Retry', async () => {
    const memberTrack = vi
      .fn<TeamApi['memberTrack']>()
      .mockRejectedValueOnce(new ApiError(500, 'internal', 'database is locked'))
      .mockResolvedValueOnce([{ member: 'rev', meetings: 2 } as MemberTrack])
    const { wrapper } = await render(memberTrack)
    expect(wrapper.text()).toContain('Track records could not be read: database is locked')
    expect(wrapper.find('.member__muted [aria-hidden="true"]').text()).toBe('—')
    expect(wrapper.find('.member__muted .sr-only').text()).toBe('Could not be read.')
    expect(wrapper.find('[role="alert"]').classes()).toContain('members__note--error')
    expect(wrapper.text()).not.toContain('No meetings or consults yet')
    expect(wrapper.text()).toContain('Reviewer')

    const retry = wrapper.findAll('button').find((b) => b.text() === 'Retry')
    await retry?.trigger('click')
    await flushPromises()
    expect(memberTrack).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).not.toContain('could not be read')
    expect(wrapper.text()).toContain('Meetings')
  })

  it('takes a 404 (a daemon without track records) as none yet', async () => {
    const { wrapper } = await render(() =>
      Promise.reject(new ApiError(404, 'not_found', 'no such endpoint')),
    )
    expect(wrapper.text()).toContain('No meetings or consults yet')
    expect(wrapper.text()).not.toContain('could not be read')
  })
})
