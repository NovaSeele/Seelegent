import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import type { GateResult, GateStep, TaskProof, Turn, VerifierResult } from '@/api/types'
import { useConnectionStore } from '@/stores/connection'
import { useTasksStore } from '@/stores/tasks'
import { setupApp } from '@/test/app'
import { iso, makeDetail, makeEvent, makeTask } from '@/test/fixtures'

import {
  budgetUsedUp,
  failedSteps,
  fixTurnsOf,
  gateOf,
  gateTone,
  gateUrgent,
  hasGate,
  queuePlace,
  stepCommand,
  stepKindKey,
  stepNote,
  stepWhat,
} from './gate'
import GateSteps from './GateSteps.vue'
import GatePanel from './GatePanel.vue'

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
  outsideDiff: false,
  snapshotTruncated: false,
  gate: null,
  verifier: null,
  ...extra,
})

const step = (extra: Partial<GateStep>): GateStep => ({
  kind: 'task',
  label: 'test',
  state: 'green',
  ...extra,
})

const result = (extra: Partial<GateResult>): GateResult => ({
  state: 'green',
  steps: [],
  notRun: [],
  warnings: [],
  startedAt: iso(0),
  ...extra,
})

const proof: TaskProof = {
  gate: true,
  commands: [{ cmd: 'npm test', kind: 'task', taskIds: ['T001'] }],
  lane: { paths: ['src/'], source: 'lead', forced: ['t7'] },
  verify: true,
  doctrine: [],
  barChanged: [],
  autoFixes: 2,
  budget: 2,
  feature: 'specs/001-x',
}

const red = result({
  state: 'red',
  steps: [
    step({ kind: 'lane', label: 'lane', state: 'green' }),
    step({ cmd: 'npm test', dir: 'web', state: 'red', exit: 1, tail: 'FAIL <b>x</b>' }),
  ],
  notRun: ['build'],
  autoFix: 2,
})

describe('gate model', () => {
  it('takes state and phase from the summary and the steps from the latest gated turn', () => {
    const detail = makeDetail({
      turns: [turn(1, { gate: red }), turn(2, { by: 'gate' })],
      proof,
    })
    const v = gateOf(
      makeTask({ gate: { state: 'running', phase: 'running', autoFixes: 1, turn: 2 } }),
      detail,
    )
    expect(v.phase).toBe('running')
    expect(v.state).toBe('running')
    expect(v.turn).toBe(2)
    expect(v.result).toBe(red)
    expect(v.fixTurns).toBe(1)
    expect(hasGate(v)).toBe(true)
    // A gate at work is never urgent, even when its last result was red.
    expect(gateUrgent(v)).toBe(false)
    expect(gateTone(v.state, v.phase)).toBe('running')
  })

  it('falls back to the latest turn without a summary gate, and is urgent on red or a rejection', () => {
    const v = gateOf(makeTask(), makeDetail({ turns: [turn(1, { gate: red })] }))
    expect([v.state, v.turn, v.phase]).toEqual(['red', 1, null])
    expect(gateUrgent(v)).toBe(true)
    expect(gateTone('red', null)).toBe('danger')

    const reject: VerifierResult = { verdict: 'reject', reasons: ['no test'], cites: [] }
    const g = gateOf(
      makeTask(),
      makeDetail({ turns: [turn(1, { gate: result({}), verifier: reject })] }),
    )
    expect(g.state).toBe('green')
    expect(gateUrgent(g)).toBe(true)
  })

  it('tells what a queued gate waits for, and its place in line from the newest gate event', () => {
    const slot = gateOf(
      makeTask({ gate: { state: 'running', phase: 'queued', wait: 'slot', autoFixes: 0 } }),
      null,
    )
    expect([slot.phase, slot.wait]).toEqual(['queued', 'slot'])
    expect(gateTone(slot.state, slot.phase, slot.wait)).toBe('neutral')
    expect(gateTone('running', 'queued', 'memory')).toBe('attention')
    // A wait left over on another phase is not shown.
    const running = gateOf(
      makeTask({ gate: { state: 'running', phase: 'running', wait: 'slot', autoFixes: 0 } }),
      null,
    )
    expect(running.wait).toBeNull()

    const gate = (seq: number, data: Record<string, unknown>) => makeEvent('t1', seq, data, 'gate')
    expect(
      queuePlace([
        gate(1, { phase: 'queued', wait: 'slot', position: 3, limit: 2 }),
        gate(2, { phase: 'queued', wait: 'slot', position: 1, limit: 2 }),
      ]),
    ).toEqual({ position: 1, limit: 2 })
    expect(
      queuePlace([
        gate(1, { phase: 'queued', wait: 'slot', position: 3, limit: 2 }),
        gate(2, { phase: 'start', steps: [] }),
      ]),
    ).toBeNull()
    expect(queuePlace(undefined)).toBeNull()
  })

  it('has nothing to show for a task without a gate', () => {
    const v = gateOf(makeTask(), makeDetail({ turns: [turn(1)] }))
    expect(hasGate(v)).toBe(false)
    expect(hasGate(gateOf(makeTask(), null))).toBe(false)
  })

  it('counts only the fix turns after the last lead turn', () => {
    expect(
      fixTurnsOf([
        turn(1, { by: 'gate' }),
        turn(2),
        turn(3, { by: 'gate' }),
        turn(4, { by: 'verifier' }),
      ]),
    ).toBe(2)
    expect(fixTurnsOf([turn(1)])).toBe(0)
  })

  it('names the folder a command runs in', () => {
    expect(stepCommand(step({ cmd: 'go test ./...', dir: 'api' }))).toBe('cd api && go test ./...')
    expect(stepCommand(step({ cmd: 'make' }))).toBe('make')
    expect(stepCommand(step({ kind: 'lane' }))).toBe('')
    // The kind already names lane, hygiene and syntax steps; a doctrine check keeps its id.
    expect(stepNote(step({ kind: 'lane', label: 'lane' }))).toBe('')
    expect(stepNote(step({ kind: 'doctrine', label: 'vet' }))).toBe('vet')
    expect(stepWhat(step({ cmd: 'make', taskIds: ['T001', 'T002'] }))).toBe('make · T001, T002')
    // Gate events from before the plan name their steps by label only.
    expect(stepKindKey({ label: 'syntax' })).toBe('task.gate.kind.auto')
    expect(stepKindKey({ label: 'npm test' })).toBeNull()
  })

  it('tells a failed gate by kind and what each step ran or checked, foreign steps included', () => {
    const g = result({
      state: 'red',
      steps: [
        step({ kind: 'lane', label: 'lane', state: 'red', files: ['a.go', 'b.go'] }),
        step({
          kind: 'auto',
          label: 'syntax',
          state: 'red',
          failures: [{ file: 'c.json', message: 'x' }],
        }),
        step({ cmd: 'npm test', state: 'error', taskIds: ['T001'] }),
        step({
          kind: 'doctrine',
          label: 'api',
          cmd: 'make',
          state: 'red',
          foreign: true,
          causedBy: [{ taskId: 't9', title: 'API rename' }],
        }),
      ],
    })
    expect(failedSteps(g)).toEqual([
      { kindKey: 'task.gate.kind.lane', what: 'a.go, b.go' },
      { kindKey: 'task.gate.kind.auto', what: 'c.json' },
      { kindKey: 'task.gate.kind.task', what: 'npm test · T001' },
      { kindKey: 'task.gate.kind.doctrine', what: 'api', causedBy: 'API rename' },
    ])
    const view = gateOf(makeTask(), makeDetail({ turns: [turn(1, { by: 'gate' })], proof }))
    expect(budgetUsedUp(view)).toBe(true)
    expect(budgetUsedUp({ ...view, proof: { ...proof, autoFixes: 0 }, fixTurns: 0 })).toBe(false)
  })
})

describe('GatePanel', () => {
  async function mountPanel(task = makeTask({ id: 't1', status: 'done' }), detail = makeDetail()) {
    const { plugins } = await setupApp()
    return mount(GatePanel, {
      props: { task, detail },
      global: { plugins, stubs: { RouterLink: RouterLinkStub } },
    })
  }

  it('names the task a foreign failure may be caused by, as a link to it', async () => {
    const { plugins } = await setupApp()
    const wrapper = mount(GateSteps, {
      props: {
        turn: 1,
        result: result({
          state: 'red',
          steps: [
            step({
              kind: 'doctrine',
              label: 'api-name',
              cmd: 'make api',
              state: 'red',
              foreign: true,
              causedBy: [{ taskId: 't9', title: 'API rename' }],
            }),
          ],
        }),
      },
      global: { plugins, stubs: { RouterLink: RouterLinkStub } },
    })
    const link = wrapper.findComponent(RouterLinkStub)
    expect(link.props('to')).toEqual({ name: 'tasks', params: { taskId: 't9' } })
    expect(link.text()).toBe('API rename')
    expect(wrapper.find('.steps__note').text()).toBe(
      'May be caused by API rename: it failed only in files that task changed. No automatic fix was started for it.',
    )
  })

  it('is hidden for a task without a gate', async () => {
    const wrapper = await mountPanel(makeTask(), makeDetail({ turns: [turn(1)] }))
    expect(wrapper.find('details').exists()).toBe(false)
  })

  it('opens on a red gate with the failing output as text, the fix turns and what the gate checks', async () => {
    const wrapper = await mountPanel(
      makeTask({ id: 't1', status: 'done', gate: { state: 'red', autoFixes: 2, turn: 3 } }),
      makeDetail({
        turns: [turn(1), turn(2, { by: 'gate' }), turn(3, { by: 'gate', gate: red })],
        proof,
      }),
    )
    const details = wrapper.find('details')
    expect(details.attributes('open')).toBeDefined()
    expect(details.classes()).toContain('tone-danger')
    // Both fixes are spent: the summary says so and the body says what is left to do.
    expect(wrapper.find('summary').text()).toBe('Gatefailedturn 3automatic fixes used up (2/2)')
    const text = wrapper.text()
    expect(text).toContain('send a follow-up or take over the task')
    expect(text).toContain('Steps after turn 3')
    expect(text).toContain('cd web && npm test')
    expect(text).toContain('exit 1')
    expect(text).toContain('Not run: build')
    // Output is text: the markup is shown, never rendered.
    expect(wrapper.find('pre').text()).toBe('FAIL <b>x</b>')
    expect(wrapper.find('pre b').exists()).toBe(false)
    expect(text).toContain('src/')
    expect(text).toContain('claimed over t7')
    expect(text).toContain('2 of 2 used')
    // The spec task of a command reads apart from it.
    expect(wrapper.find('.facts__command').text()).toBe('npm test · T001')
    expect(wrapper.findComponent(RouterLinkStub).props('to')).toEqual({ name: 'proof' })
  })

  it('shows the review at work and the verifier verdict with unmatched citations', async () => {
    const verifier: VerifierResult = {
      verdict: 'reject',
      reasons: ['The test never runs the new path.'],
      cites: [
        { path: 'a.go', line: 3, state: 'verified' },
        { path: 'b.go', line: 9, state: 'quote_mismatch' },
      ],
    }
    const wrapper = await mountPanel(
      makeTask({
        id: 't1',
        status: 'running',
        gate: { state: 'green', phase: 'verifying', autoFixes: 0, turn: 1 },
      }),
      makeDetail({ turns: [turn(1, { gate: result({}), verifier })], proof }),
    )
    expect(wrapper.find('summary').text()).toContain('second-opinion review')
    expect(wrapper.find('details').classes()).toContain('tone-running')
    const text = wrapper.text()
    expect(text).toContain('Rejected')
    expect(text).toContain('The test never runs the new path.')
    expect(text).toContain('1 cited lines do not match the repository:')
    expect(text).toContain('b.go:9')
    expect(text).not.toContain('a.go:3')
  })
})

describe('GatePanel queued', () => {
  async function mountQueued(wait: 'slot' | 'memory') {
    const { plugins } = await setupApp()
    useConnectionStore().settings = { gateConcurrency: 2 } as never
    const task = makeTask({
      id: 't1',
      status: 'running',
      gate: { state: 'running', phase: 'queued', wait, autoFixes: 0, turn: 1 },
    })
    return mount(GatePanel, {
      props: { task, detail: makeDetail() },
      global: { plugins, stubs: { RouterLink: RouterLinkStub } },
    })
  }

  it('says a gate waits for a slot, with how many run at once, without a spinner', async () => {
    const wrapper = await mountQueued('slot')
    const summary = wrapper.get('summary')
    expect(summary.text()).toContain('waiting for a gate slot')
    expect(summary.text()).toContain('2 at a time')
    expect(summary.find('[role="status"]').exists()).toBe(false)
    expect(wrapper.get('details').classes()).toContain('tone-neutral')
  })

  it('gives the place in line when the task’s log is on screen', async () => {
    const { plugins } = await setupApp()
    const tasks = useTasksStore()
    tasks.setApi({
      events: () =>
        Promise.resolve({
          items: [
            makeEvent('t1', 1, { phase: 'queued', wait: 'slot', position: 2, limit: 3 }, 'gate'),
          ],
          lastSeq: 1,
        }),
    } as never)
    const log = tasks.retainEvents('t1')
    await flushPromises()
    expect(log.items.value).toHaveLength(1)
    const task = makeTask({
      id: 't1',
      status: 'running',
      gate: { state: 'running', phase: 'queued', wait: 'slot', autoFixes: 0 },
    })
    const wrapper = mount(GatePanel, {
      props: { task, detail: makeDetail() },
      global: { plugins, stubs: { RouterLink: RouterLinkStub } },
    })
    expect(wrapper.get('summary').text()).toContain('number 2 in line, 3 at a time')
    tasks.releaseEvents('t1')
  })

  it('says a gate waits for free memory', async () => {
    const wrapper = await mountQueued('memory')
    expect(wrapper.get('summary').text()).toContain('waiting for free memory')
    expect(wrapper.get('summary').text()).not.toContain('at a time')
    expect(wrapper.get('details').classes()).toContain('tone-attention')
  })
})

describe('GateSteps', () => {
  async function mountSteps(r: GateResult) {
    const { plugins } = await setupApp()
    return mount(GateSteps, { props: { result: r, turn: 2 }, global: { plugins } })
  }

  it('leaves the output closed when the failures are listed, and says not run only once', async () => {
    const wrapper = await mountSteps(
      result({
        state: 'red',
        steps: [
          step({
            kind: 'auto',
            label: 'syntax',
            state: 'red',
            failures: [{ file: 'a.json', message: 'bad json' }],
            tail: 'a.json: bad json',
          }),
          step({ cmd: 'make', state: 'not_run' }),
        ],
        notRun: ['make'],
      }),
    )
    expect(wrapper.text()).toContain('bad json')
    expect(wrapper.find('details').attributes('open')).toBeUndefined()
    expect(wrapper.text()).not.toContain('Not run: make')
  })

  it('says a step the memory watchdog stopped, with its stops, and a run that ended for it', async () => {
    const wrapper = await mountSteps(
      result({
        state: 'error',
        reason: 'low_memory',
        steps: [step({ cmd: 'go test ./...', state: 'not_run', stopped: 'low_memory', stops: 3 })],
      }),
    )
    expect(wrapper.get('[data-stopped]').text()).toBe(
      'Stopped because the machine ran low on memory (3 of 3); the gate runs again once memory is back.',
    )
    expect(wrapper.get('[data-reason]').text()).toBe(
      'The machine kept running out of memory: the gate was stopped 3 times.',
    )
  })
})
