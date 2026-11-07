import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import type { RolesApi } from '@/api/roles'
import type { Role, RoleList, Skill } from '@/api/types'
import { useConnectionStore } from '@/stores/connection'
import { useRolesStore } from '@/stores/roles'
import { setupApp } from '@/test/app'

import RolesPage from './RolesPage.vue'

const qa: Role = {
  id: 'qa',
  name: 'QA',
  description: '',
  rules: '',
  outputStyle: '',
  skills: ['tdd'],
  preferredAccounts: [],
  scope: 'global',
  overridesGlobal: false,
}

const skill = (name: string): Skill => ({ name, description: '', root: 'user' })

describe('RolesPage', () => {
  it('reads the installed skills again on each visit and after a resync', async () => {
    const { plugins } = await setupApp()
    const resyncs: (() => void)[] = []
    vi.spyOn(useConnectionStore(), 'onResync').mockImplementation((handler) => {
      resyncs.push(handler)
      return () => resyncs.splice(resyncs.indexOf(handler), 1)
    })
    let onDisk: Skill[] = []
    const skills = vi.fn(() => Promise.resolve(onDisk))
    useRolesStore().setApi({
      list: () => Promise.resolve<RoleList>({ items: [qa], projects: [] }),
      skills,
    } as unknown as RolesApi)

    const first = mount(RolesPage, { global: { plugins } })
    await flushPromises()
    expect(first.find('.skill--missing').exists()).toBe(true)

    // Installed outside the dashboard; a resync shows it.
    onDisk = [skill('tdd')]
    resyncs.forEach((handler) => handler())
    await flushPromises()
    expect(first.find('.skill--missing').exists()).toBe(false)
    first.unmount()

    // Removed again; opening the page again reads afresh instead of using the store's copy.
    onDisk = []
    const calls = skills.mock.calls.length
    const second = mount(RolesPage, { global: { plugins } })
    await flushPromises()
    expect(skills.mock.calls.length).toBeGreaterThan(calls)
    expect(second.find('.skill--missing').exists()).toBe(true)
    second.unmount()
  })
})
