/**
 * Regression tests of the issues views from the M15 live check: the access panel while the daemon has not answered
 * (F38), and the checks of a ticket that is finished (F39).
 */
import { mount, RouterLinkStub } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { createApiClient, createGitHubApi } from '@/api'
import type { GitHubAccess, Ticket } from '@/api/types'
import { useGitHubStore } from '@/stores/github'
import { flush, setupApp } from '@/test/app'

import AccessPanel from './AccessPanel.vue'
import TicketDetail from './TicketDetail.vue'
import TicketSections from './TicketSections.vue'

const json = (body: unknown) =>
  new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })

function makeTicket(overrides: Partial<Ticket> = {}): Ticket {
  return {
    id: 'tk1',
    repoId: 'r1',
    issueKey: '#3',
    issue: { number: 3, title: 'Cache never expires' },
    branch: 'seelegent/3-cache',
    worktree: '/srv/wt/tk1',
    status: 'pr_open',
    taskIds: ['t1'],
    pr: { number: 7, state: 'open', draft: false, url: 'https://github.com/o/r/pull/7' },
    checks: {
      state: 'failure',
      runs: [{ kind: 'check', name: 'build', status: 'completed', conclusion: 'failure' }],
    },
    createdAt: '2026-11-08T00:00:00Z',
    updatedAt: '2026-11-08T00:00:00Z',
    slug: '3-cache',
    base: 'main',
    env: null,
    outbox: [],
    checksFix: { taskId: 't9', runs: ['build'], at: '2026-11-08T00:00:00Z' },
    worktreeRemoved: false,
    tasks: [],
    gates: [],
    files: [],
    log: [],
    ...overrides,
  }
}

describe('issues regressions', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  it('shows no tier until the daemon answers, then the real one (F38)', async () => {
    const { plugins } = await setupApp()
    let answer: (a: GitHubAccess) => void = () => {}
    const fetch = vi.fn<typeof globalThis.fetch>((input) => {
      if (String(input).includes('/github/access')) {
        return new Promise<Response>((resolve) => {
          answer = (a) => resolve(json(a))
        })
      }
      return Promise.resolve(json([]))
    })
    const gh = useGitHubStore()
    gh.setApi(createGitHubApi(createApiClient({ fetch })))
    const w = mount(AccessPanel, { global: { plugins, stubs: { RouterLink: RouterLinkStub } } })
    void gh.refreshAccess()
    await flush()
    expect(w.text()).not.toContain('Manual')
    expect(w.text()).not.toContain('No GitHub sign-in')
    expect(w.text()).not.toContain('github.com')

    answer({
      tier: 'token',
      hosts: ['github.com', 'ghe.example.com'],
      checkedAt: '2026-11-08T00:00:00Z',
    })
    await vi.waitFor(() => expect(w.text()).toContain('Token'))
    expect(w.text()).toContain('ghe.example.com')
    expect(w.text()).not.toContain('Manual')
  })

  it('drops the last checks state and the fix link once the ticket is finished (F39)', async () => {
    const { plugins } = await setupApp()
    const merged = makeTicket({ status: 'done', pr: { number: 7, state: 'merged', draft: false } })
    const fetch = vi.fn<typeof globalThis.fetch>((input) =>
      Promise.resolve(json(String(input).includes('/github/tickets/tk1') ? merged : [])),
    )
    useGitHubStore().setApi(createGitHubApi(createApiClient({ fetch })))
    const w = mount(TicketDetail, {
      props: { ticketId: 'tk1' },
      global: { plugins, stubs: { RouterLink: RouterLinkStub } },
    })
    await vi.waitFor(() => expect(w.text()).toContain('Cache never expires'))
    expect(w.text()).not.toContain('Checks failed')
    expect(w.text()).not.toContain('Fixing build in task')

    const open = mount(TicketSections, {
      props: { ticket: makeTicket() },
      global: { plugins, stubs: { RouterLink: RouterLinkStub } },
    })
    expect(open.text()).toContain('Fixing build in task')
    const done = mount(TicketSections, {
      props: { ticket: makeTicket({ status: 'dropped' }) },
      global: { plugins, stubs: { RouterLink: RouterLinkStub } },
    })
    expect(done.text()).not.toContain('Fixing build in task')
  })
})
