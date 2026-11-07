import { mount, RouterLinkStub } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { GitApi } from '@/api'
import type { RestoreResult, Turn } from '@/api/types'
import { flush, setupApp } from '@/test/app'
import { iso, makeDetail, makeTask } from '@/test/fixtures'

import Icon from '@/components/ui/Icon.vue'

import OutsideDiffButton from '../views/git/OutsideDiffButton.vue'
import OutsideDiffDialog from '../views/git/OutsideDiffDialog.vue'
import { safetyCount, safetyOf, safetyParts, safetyRestored, safetyUrgent } from './safety'
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

/** A button of the open (teleported) dialog, by its text. */
const bodyButton = (text: string) =>
  [...(document.body.querySelector('[role="dialog"]')?.querySelectorAll('button') ?? [])].find(
    (b) => b.textContent?.trim() === text,
  )

describe('safety model', () => {
  it('groups risk per turn newest first and takes findings from the latest finished turn only', () => {
    const s = safetyOf(risky)
    expect(s.risk.map((g) => [g.turn, g.open, g.outsideDiff])).toEqual([
      [2, 2, true],
      [1, 0, true],
    ])
    expect(s.findings.map((f) => f.kind)).toEqual(['unfetched_url'])
    expect(s.findingsTurn).toBe(2)
    expect(s.blocked).toEqual([{ turn: 2, command: 'git reset --hard' }])
    // Restored work is not counted: 2 open files, 1 overlap, 1 finding, 1 refused command.
    expect(safetyCount(s)).toBe(2 + 1 + 1 + 1)
    expect(safetyParts(s)).toEqual({ risk: 2, overlaps: 1, findings: 1, blocked: 1, restored: 1 })
    expect(safetyUrgent(s)).toBe(true)
    expect(safetyRestored(s)).toBe(false)
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

  it('offers no outside diff for a turn that only lost lines to an edit tool, or has no snapshots', () => {
    const s = safetyOf(
      makeDetail({
        turns: [
          turn(1, { workAtRisk: [{ path: 'a.go', why: 'outside_tools' }], outsideDiff: false }),
          turn(2, { workAtRisk: [{ path: 'b.go', why: 'large_removal', lostLines: 9 }] }),
        ],
      }),
    )
    expect(s.risk.map((g) => [g.turn, g.outsideDiff])).toEqual([
      [2, false],
      [1, false],
    ])
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
    expect(wrapper.find('summary').text()).toBe(
      'Safety checks5 found2 files at risk1 shared file1 finding1 command refused1 restored',
    )
    expect(wrapper.find('.safety__phrase--muted').text()).toBe('1 restored')
    expect(wrapper.find('summary').findComponent(Icon).props('name')).toBe('warning')
    expect(text).toContain('nothing was changed')
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

  it('asks before it restores, then names the backup and asks for a reload', async () => {
    const result: RestoreResult = {
      restored: ['notes.md', 'big.go'],
      skipped: [],
      backup: 't2-restore-17',
    }
    const api = fakeApi({ restore: vi.fn(() => Promise.resolve(result)) })
    const wrapper = await mountPanel(api)
    const button = wrapper.findAll('button').find((b) => b.text().startsWith('Restore files'))
    await button?.trigger('click')
    await flush()
    expect(api.restore).not.toHaveBeenCalled()
    const dialog = () => document.body.querySelector('[role="dialog"]')
    expect(dialog()?.textContent).toContain('Restore files from before turn 2?')
    expect(dialog()?.textContent).toContain('replaced with what they held before turn 2')
    expect(dialog()?.textContent).toContain('A backup of the current content is kept first.')
    expect([...(dialog()?.querySelectorAll('li code') ?? [])].map((c) => c.textContent)).toEqual([
      'notes.md',
      'big.go',
    ])
    bodyButton('Restore files (2)')?.click()
    await flush()
    expect(api.restore).toHaveBeenCalledWith('t1', 2, {
      files: ['notes.md', 'big.go'],
      force: false,
    })
    expect(dialog()?.textContent).toContain('2 files restored.')
    expect(dialog()?.textContent).toContain('t2-restore-17')
    expect(document.activeElement?.textContent?.trim()).toBe('Close')
    expect(wrapper.emitted('changed')).toHaveLength(1)
  })

  it('cancels without restoring', async () => {
    const api = fakeApi()
    const wrapper = await mountPanel(api)
    await wrapper
      .findAll('button')
      .find((b) => b.text().startsWith('Restore files'))
      ?.trigger('click')
    await flush()
    bodyButton('Cancel')?.click()
    await flush()
    expect(api.restore).not.toHaveBeenCalled()
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
  })

  it('leaves files changed after the turn alone, and overwrites them only after a second question', async () => {
    const first: RestoreResult = {
      restored: ['big.go'],
      skipped: [
        { path: 'notes.md', error: 'changed since the turn ended', code: 'changed_since_turn' },
      ],
      backup: 't2-restore-1',
    }
    const second: RestoreResult = { restored: ['notes.md'], skipped: [], backup: 't2-restore-2' }
    const restore = vi
      .fn<GitApi['restore']>()
      .mockResolvedValueOnce(first)
      .mockResolvedValueOnce(second)
    const wrapper = await mountPanel(fakeApi({ restore }))
    await wrapper
      .findAll('button')
      .find((b) => b.text().startsWith('Restore files'))
      ?.trigger('click')
    await flush()
    bodyButton('Restore files (2)')?.click()
    await flush()
    const text = () => document.body.querySelector('[role="dialog"]')?.textContent ?? ''
    expect(text()).toContain('1 file restored.')
    expect(text()).toContain('notes.md · changed since the turn ended')
    expect(text()).toContain('left as they are')

    bodyButton('Restore anyway')?.click()
    await flush()
    expect(text()).toContain('Overwrite changes made after turn 2?')
    expect(restore).toHaveBeenCalledTimes(1)
    bodyButton('Overwrite (1)')?.click()
    await flush()
    expect(restore).toHaveBeenLastCalledWith('t1', 2, { files: ['notes.md'], force: true })
    expect(text()).toContain('2 files restored.')
    expect(text()).toContain('t2-restore-1')
    expect(text()).toContain('t2-restore-2')
    expect(text()).not.toContain('Restore anyway')
    expect(wrapper.emitted('changed')).toHaveLength(2)
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
    const buttons = wrapper.findAll('button').filter((b) => b.text() === 'Outside changes')
    expect(buttons).toHaveLength(2)
    await buttons[0]?.trigger('click')
    await flush()
    const dialog = wrapper.findComponent(OutsideDiffDialog)
    expect(dialog.props('turn')).toBe(2)
    expect(dialog.props('turns')).toEqual([2, 1])
    expect(dialog.props('restored')).toEqual({ 1: ['old.txt'] })
    expect(api.outsideDiff).toHaveBeenCalledWith('t1', 2, expect.any(AbortSignal))
    expect(document.body.textContent).toContain('notes.md')
  })
})

describe('SafetyPanel when every file is back', () => {
  const restoredOnly = makeDetail({
    status: 'done',
    turns: [
      turn(3, {
        workAtRisk: [{ path: 'notes.md', why: 'outside_tools', lostLines: 3, restored: true }],
      }),
    ],
    fileOverlaps: [
      { file: 'a.go', taskId: 't8', title: 'Old task', status: 'done', severity: 'recent' },
    ],
  })

  it('reads as done: past tense, a success chip, a muted count and a calm icon', async () => {
    const { plugins } = await setupApp()
    const wrapper = mount(SafetyPanel, {
      props: { task: makeTask({ id: 't1', status: 'done' }), detail: restoredOnly, api: fakeApi() },
      global: { plugins, stubs: { RouterLink: RouterLinkStub } },
    })
    const details = wrapper.find('details')
    expect(details.attributes('open')).toBeUndefined()
    expect(details.classes()).toContain('tone-neutral')
    expect(wrapper.find('summary').text()).toBe('Safety checks1 found1 shared file1 restored')
    expect(wrapper.find('summary').findComponent(Icon).props('name')).toBe('info')
    const title = wrapper.find('.risk__title')
    expect(title.text()).toBe('Work restored')
    expect(title.classes()).toContain('risk__title--done')
    expect(wrapper.text()).toContain('Files were put back from the snapshot taken before turn 3.')
    expect(wrapper.find('.risk .chip.tone-success').text()).toBe('Restored')
    expect(wrapper.text()).toContain('changed recently')
    expect(wrapper.findAll('button').map((b) => b.text())).toEqual(['Outside changes'])
  })

  it('still shows a task whose only finding was restored', async () => {
    const { plugins } = await setupApp()
    const wrapper = mount(SafetyPanel, {
      props: {
        task: makeTask({ id: 't1', status: 'done' }),
        detail: { ...restoredOnly, fileOverlaps: [] },
        api: fakeApi(),
      },
      global: { plugins },
    })
    expect(wrapper.find('summary').text()).toBe('Safety checks1 restored')
  })
})

describe('OutsideDiffButton', () => {
  it('is hidden without snapshots and opens on the newest turn that has them', async () => {
    const { plugins } = await setupApp()
    const outsideDiff = vi.fn<GitApi['outsideDiff']>(() => Promise.resolve([]))
    const none = mount(OutsideDiffButton, {
      props: {
        taskId: 't1',
        detail: makeDetail({ turns: [turn(1, { outsideDiff: false })] }),
        api: fakeApi(),
      },
      global: { plugins },
    })
    expect(none.find('button').exists()).toBe(false)

    const wrapper = mount(OutsideDiffButton, {
      props: {
        taskId: 't1',
        detail: makeDetail({ turns: [turn(1), turn(2), turn(3, { outsideDiff: false })] }),
        api: fakeApi({ outsideDiff }),
      },
      global: { plugins },
      attachTo: document.body,
    })
    await wrapper.find('button').trigger('click')
    await flush()
    expect(wrapper.findComponent(OutsideDiffDialog).props('turns')).toEqual([2, 1])
    expect(outsideDiff).toHaveBeenCalledWith('t1', 2, expect.any(AbortSignal))
  })
})

describe('OutsideDiffDialog', () => {
  it('keeps the selected turn among its options, labels them, and marks restored files', async () => {
    const { plugins } = await setupApp()
    const outsideDiff = vi.fn<GitApi['outsideDiff']>(() =>
      Promise.resolve([
        { path: 'notes.md', binary: false, truncated: false, hunks: [] },
        { path: 'x.orig', binary: false, truncated: false, hunks: [] },
      ]),
    )
    mount(OutsideDiffDialog, {
      props: {
        open: true,
        taskId: 't1',
        turns: [4, 2],
        turn: 3,
        restored: { 4: ['notes.md'] },
        api: fakeApi({ outsideDiff }),
      },
      global: { plugins },
      attachTo: document.body,
    })
    await flush()
    expect(outsideDiff).toHaveBeenCalledWith('t1', 4, expect.any(AbortSignal))
    const select = document.body.querySelector('select')
    expect(select?.value).toBe('4')
    expect([...(select?.options ?? [])].map((o) => o.textContent?.trim())).toEqual([
      'Turn 4',
      'Turn 2',
    ])
    expect(document.body.querySelector('.select__label')?.textContent?.trim()).toBe('Turn')
    const heads = [...document.body.querySelectorAll('.outside__head')].map((h) =>
      h.textContent?.replace(/\s+/g, ' ').trim(),
    )
    expect(heads).toEqual(['notes.mdRestored', 'x.orig'])
  })

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
