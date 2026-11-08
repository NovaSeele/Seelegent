import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { AutomationsApi } from '@/api'
import type { Automation, AutomationRun } from '@/api/types'
import { useAutomationsStore } from '@/stores/automations'
import { flush, setupApp } from '@/test/app'

import AutomationRuns from './AutomationRuns.vue'

const automation = (id: string, name: string) => ({ id, name }) as Automation
const run = (id: number, detail: string): AutomationRun => ({
  id,
  at: '2026-11-07T07:30:00.000Z',
  trigger: 'schedule',
  outcome: 'error',
  detail,
})

describe('AutomationRuns', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  it('shows the runs of the automation it is open for, not a slow answer for the one before (E-B17)', async () => {
    const { plugins } = await setupApp()
    let releaseA: (runs: AutomationRun[]) => void = () => undefined
    const runs = vi.fn((id: string) =>
      id === 'a1'
        ? new Promise<AutomationRun[]>((resolve) => (releaseA = resolve))
        : Promise.resolve([run(2, 'the run of B')]),
    )
    useAutomationsStore().setApi({ runs } as unknown as AutomationsApi)
    const wrapper = mount(AutomationRuns, {
      props: { open: false, automation: automation('a1', 'Nightly') },
      attachTo: document.body,
      global: { plugins, stubs: { RouterLink: true } },
    })
    await wrapper.setProps({ open: true })
    await flush()
    // Closed while A's runs are still on the way, then opened for B.
    await wrapper.setProps({ open: false })
    await wrapper.setProps({ automation: automation('b1', 'Weekly'), open: true })
    await flush()
    releaseA([run(1, 'the run of A')])
    await flush()
    const dialog = document.querySelector('[role=dialog]') as HTMLElement
    await vi.waitFor(() => expect(dialog.textContent).toContain('the run of B'))
    expect(dialog.textContent).toContain('Runs of Weekly')
    expect(dialog.textContent).not.toContain('the run of A')
    expect(runs.mock.calls.map(([id]) => id)).toEqual(['a1', 'b1'])
    wrapper.unmount()
  })
})
