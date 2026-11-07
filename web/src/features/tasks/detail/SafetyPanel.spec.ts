import { mount, RouterLinkStub } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { GitApi } from '@/api'
import type { RestoreResult, Turn } from '@/api/types'
import { flush, setupApp } from '@/test/app'
import { iso, makeDetail, makeTask } from '@/test/fixtures'

import OutsideDiffDialog from '../views/git/OutsideDiffDialog.vue'
import { safetyCount, safetyOf, safetyUrgent } from './safety'
import SafetyPanel from './SafetyPanel.vue'

const turn = (n: number, extra: Partial<Turn> = {}): Turn => ({
  n,
  by: 'lead',
  message: `m${n}`,
  status: 'ok',
  startedAt: iso(n * 1000),
  files: [],
  commands: [],
  blockedCommands: [],
  deniedActions: [],
  usage: null,
  attempts: [],
  git: null,
  workAtRisk: [],
  hygiene: [],
  scope: [],
  outsideDiff: true,
  ...extra,
})

const risky = makeDetail({
  status: 'done',
  turns: [
    turn(1, {
      workAtRisk: [{ path: 'old.txt', why: 'outside_tools', lostLines: 2, restored: true }],
      hygiene: [{ kind: 'junk_file', severity: 'red', path: 'stale.orig' }],
    }),
    turn(2, {
      workAtRisk: [
        { path: 'notes.md', why: 'outside_tools', lostLines: 3 },
        { path: 'big.go', why: 'large_removal', lostLines: 40 },
      ],
      scope: [{ kind: 'unfetched_url', severity: 'warn', detail: 'https://example.com/x' }],
      blockedCommands: ['git reset --hard'],
    }),
  ],
  fileOverlaps: [
    { file: 'notes.md', taskId: 't9', title: 'Other task', status: 'running', severity: 'active' },
  ],
})

function fakeApi(overrides: Partial<GitApi> = {}): GitApi {
  const missing = () => Promise.reject(new Error('unexpected call'))
  return {
    status: vi.fn(missing),
    diff: vi.fn(missing),
    suggestMessage: vi.fn(missing),
    dismissSuggestion: vi.fn(missing),
    commit: vi.fn(missing),
    outsideDiff: vi.fn(missing),
    restore: vi.fn(missing),
    ...overrides,
  }
}

afterEach(() => {
  document.body.innerHTML = ''
})

describe('safety model', () => {
  it('groups risk per turn newest first and takes findings from the latest finished turn only', () => {
    const s = safetyOf(risky)
    expect(s.risk.map((g) => [g.turn, g.open])).toEqual([
      [2, 2],
      [1, 0],
    ])
    expect(s.findings.map((f) => f.kind)).toEqual(['unfetched_url'])
    expect(s.findingsTurn).toBe(2)
    expect(s.blocked).toEqual([{ turn: 2, command: 'git reset --hard' }])
    expect(safetyCount(s)).toBe(3 + 1 + 1 + 1)
    expect(safetyUrgent(s)).toBe(true)
  })

  it('is empty and calm for a clean task, and skips a running turn for findings', () => {
    const clean = makeDetail({
      turns: [
        turn(1, { hygiene: [{ kind: 'eol_changed', severity: 'red', path: 'a' }] }),
        turn(2, { status: 'running' }),
      ],
    })
    const s = safetyOf(clean)
    expect(s.findingsTurn).toBe(1)
    expect(safetyOf(makeDetail()).risk).toEqual([])
    expect(safetyCount(safetyOf(null))).toBe(0)
    expect(safetyUrgent(safetyOf(makeDetail({ turns: [turn(1)] })))).toBe(false)
  })
})

describe('SafetyPanel', () => {
  async function mountPanel(api: GitApi, status: 'done' | 'running' = 'done', detail = risky) {
    const { plugins } = await setupApp()
    return mount(SafetyPanel, {
      props: { task: makeTask({ id: 't1', status }), detail, api },
      global: { plugins, stubs: { RouterLink: RouterLinkStub } },
      attachTo: document.body,
    })
  }

  it('shows nothing when the checks found nothing', async () => {
    const wrapper = await mountPanel(fakeApi(), 'done', makeDetail({ turns: [turn(1)] }))
    expect(wrapper.find('details').exists()).toBe(false)
  })

  it('opens on urgent findings and lists risk, overlaps, findings and refused commands', async () => {
    const wrapper = await mountPanel(fakeApi())
    const details = wrapper.find('details')
    expect(details.attributes('open')).toBeDefined()
    const text = wrapper.text()
    expect(text).toContain('Work may be lost')
    expect(text).toContain('notes.md')
    expect(text).toContain('uncommitted lines lost: 3')
    expect(text).toContain('Restored')
    expect(text).toContain('Other task')
    expect(text).toContain('URL the worker never opened')
    expect(text).toContain('git reset --hard')
    expect(wrapper.findComponent(RouterLinkStub).props('to')).toEqual({
      name: 'tasks',
      params: { taskId: 't9' },
    })
    // Turn 1 is fully restored: only turn 2 offers Restore.
    const restore = wrapper.findAll('button').filter((b) => b.text().startsWith('Restore files'))
    expect(restore.map((b) => b.text())).toEqual(['Restore files (2)'])
  })

  it('restores a turn and asks for a reload', async () => {
    const result: RestoreResult = { restored: ['notes.md', 'big.go'], skipped: [] }
    const api = fakeApi({ restore: vi.fn(() => Promise.resolve(result)) })
    const wrapper = await mountPanel(api)
    const button = wrapper.findAll('button').find((b) => b.text().startsWith('Restore files'))
    await button?.trigger('click')
    await flush()
    expect(api.restore).toHaveBeenCalledWith('t1', 2)
    expect(wrapper.emitted('changed')).toHaveLength(1)
  })

  it('waits for the worker to stop before it restores', async () => {
    const wrapper = await mountPanel(fakeApi(), 'running')
    const button = wrapper.findAll('button').find((b) => b.text().startsWith('Restore files'))
    expect(button?.attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('once the worker has stopped')
  })

  it('opens the outside diff of a turn', async () => {
    const api = fakeApi({
      outsideDiff: vi.fn(() =>
        Promise.resolve([{ path: 'notes.md', binary: false, truncated: false, hunks: [] }]),
      ),
    })
    const wrapper = await mountPanel(api)
    const button = wrapper
      .findAll('button')
      .find((b) => b.text() === 'Changes outside the edit tools')
    await button?.trigger('click')
    await flush()
    const dialog = wrapper.findComponent(OutsideDiffDialog)
    expect(dialog.props('turn')).toBe(2)
    expect(api.outsideDiff).toHaveBeenCalledWith('t1', 2, expect.any(AbortSignal))
    expect(document.body.textContent).toContain('notes.md')
  })
})

describe('OutsideDiffDialog', () => {
  it('says when nothing changed and shows a failed read with a retry', async () => {
    const { plugins } = await setupApp()
    const outsideDiff = vi
      .fn<GitApi['outsideDiff']>()
      .mockRejectedValueOnce(new Error('no snapshots'))
      .mockResolvedValueOnce([])
    mount(OutsideDiffDialog, {
      props: { open: true, taskId: 't1', turns: [3, 2, 1], api: fakeApi({ outsideDiff }) },
      global: { plugins },
      attachTo: document.body,
    })
    await flush()
    expect(outsideDiff).toHaveBeenCalledWith('t1', 3, expect.any(AbortSignal))
    expect(document.body.textContent).toContain('Could not read the changes: no snapshots')
    const retry = [...document.body.querySelectorAll('button')].find(
      (b) => b.textContent?.trim() === 'Retry',
    )
    retry?.click()
    await flush()
    expect(document.body.textContent).toContain('Nothing changed outside the edit tools.')
  })
})
