import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/api/client'
import type { TeamApi } from '@/api/team'
import type { ContestDiff } from '@/api/types'
import { useTeamStore } from '@/stores/team'
import { setupApp } from '@/test/app'

import ContestDiffView from './ContestDiffView.vue'

async function mountView(contestDiff: TeamApi['contestDiff']) {
  const { plugins } = await setupApp()
  useTeamStore().setApi({ contestDiff } as unknown as TeamApi)
  const wrapper = mount(ContestDiffView, {
    props: { contestId: 'c1', label: 'V2' },
    global: { plugins },
  })
  await flushPromises()
  return wrapper
}

describe('ContestDiffView', () => {
  it('says when a removed variant has no stored copy, never "no changes"', async () => {
    const gone = new ApiError(404, 'not_found', 'gone', {
      reason: 'variant_data_gone',
      label: 'V2',
    })
    const wrapper = await mountView(vi.fn(() => Promise.reject(gone)))
    expect(wrapper.text()).toContain('The changes of V2 are no longer available')
    expect(wrapper.text()).not.toContain('No changes')
  })

  it('says when the stored copy is damaged', async () => {
    const bad = new ApiError(409, 'bad_state', 'bad', {
      reason: 'variant_data_damaged',
      label: 'V2',
    })
    const wrapper = await mountView(vi.fn(() => Promise.reject(bad)))
    expect(wrapper.text()).toContain('The stored changes of V2 are damaged')
  })

  it('says when the changes were cut at the size limit', async () => {
    const diff: ContestDiff = {
      id: 'c1',
      label: 'V2',
      base: 'b',
      diff: '',
      truncated: true,
      files: [
        {
          path: 'a.txt',
          status: 'modified',
          binary: false,
          truncated: true,
          additions: 1,
          deletions: 0,
          hunks: [],
        },
      ],
    } as unknown as ContestDiff
    const wrapper = await mountView(vi.fn(() => Promise.resolve(diff)))
    expect(wrapper.text()).toContain('the rest is not shown')
  })
})
