import { mount, RouterLinkStub } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, ref } from 'vue'

import { createApiClient } from '@/api'
import { createTeamApi } from '@/api/team'
import type { Dissent, Meeting } from '@/api/types'
import ToastContainer from '@/components/ui/ToastContainer.vue'
import { useTeamStore } from '@/stores/team'
import { useToastStore } from '@/stores/toast'
import { flush, setupApp } from '@/test/app'

import MeetingOutcome from './MeetingOutcome.vue'

const dissent = (outcome?: Dissent['outcome']): Dissent => ({
  id: 'd1',
  label: 'B',
  member: 'sam',
  kind: 'dissent',
  text: 'The cache will go stale.',
  claims: [],
  watchPaths: [],
  signals: [],
  ...(outcome ? { outcome } : {}),
})

const meeting = (outcome?: Dissent['outcome']): Meeting =>
  ({
    id: 'm1',
    title: 'Cache',
    question: 'Which cache?',
    options: [],
    context: [],
    cwd: '/srv/app',
    repoRoot: '/srv/app',
    status: 'closed',
    maxRounds: 2,
    round: 2,
    sealed: null,
    attendees: [],
    rounds: [],
    leadQa: [],
    leadNotes: [],
    decision: { text: 'Use the LRU.' },
    dissents: [dissent(outcome)],
    adviceNotTaken: [],
    actions: [],
    estimate: null,
    usage: { runs: 0, byBackend: {}, tokens: 0 },
    dissentRuns: [],
    createdAt: '2026-11-08T00:00:00Z',
    updatedAt: '2026-11-08T00:00:00Z',
  }) as unknown as Meeting

describe('MeetingOutcome undo', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  it('takes the "vindicated" toast away when Undo reopens the dissent', async () => {
    const { plugins } = await setupApp()
    const fetch = vi.fn<typeof globalThis.fetch>(async (_input, init) => {
      const { outcome } = JSON.parse(String(init?.body)) as { outcome: string }
      const next = meeting(outcome === 'open' ? undefined : (outcome as Dissent['outcome']))
      return new Response(JSON.stringify(next), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    })
    useTeamStore().setApi(createTeamApi(createApiClient({ fetch })))
    const Host = defineComponent(() => {
      const current = ref(meeting())
      return () =>
        h('div', [
          h(MeetingOutcome, {
            meeting: current.value,
            onUpdated: (m: Meeting) => (current.value = m),
          }),
          h(ToastContainer),
        ])
    })
    mount(Host, {
      attachTo: document.body,
      global: { plugins, stubs: { RouterLink: RouterLinkStub } },
    })
    const button = (text: string) =>
      [...document.querySelectorAll<HTMLButtonElement>('button')].find(
        (b) => b.textContent?.trim() === text,
      )
    button('Vindicate')?.click()
    await vi.waitFor(() => expect(useToastStore().toasts).toHaveLength(1))
    await flush()
    expect(document.querySelector('.toast')?.textContent).toContain('vindicated')

    button('Undo')?.click()
    await vi.waitFor(() => expect(button('Vindicate')).toBeDefined())
    await flush()
    expect(useToastStore().toasts.map((t) => t.message)).not.toContainEqual(
      expect.stringContaining('vindicated'),
    )
    expect(document.querySelector('.toast')?.textContent ?? '').not.toContain('vindicated')
  })
})
