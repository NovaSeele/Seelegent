import { mount, RouterLinkStub } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import { useTasksStore } from '@/stores/tasks'
import { setupApp } from '@/test/app'
import { iso, makeTask, T0 } from '@/test/fixtures'

import TaskRow from './TaskRow.vue'

async function rowAt(createdAt: string, now: number) {
  const { plugins } = await setupApp()
  useTasksStore().upsert(makeTask({ id: 't1', createdAt }))
  return mount(TaskRow, {
    props: {
      id: 't1',
      now,
      current: false,
      focusable: true,
      selectMode: false,
      selected: false,
      showProject: false,
    },
    global: { plugins, stubs: { RouterLink: RouterLinkStub } },
  })
}

describe('TaskRow', () => {
  // The list's clock ticks every 30 s, so a task created since the last tick is ahead of it (M18 hunt H17).
  it('shows a task newer than the clock as just now, never in the future', async () => {
    const row = await rowAt(iso(11_000), T0)
    expect(row.find('time').text()).toBe('just now')
  })

  it('shows an older task by its age', async () => {
    const row = await rowAt(iso(-5 * 60_000), T0)
    expect(row.find('time').text()).toBe('5 minutes ago')
  })
})
