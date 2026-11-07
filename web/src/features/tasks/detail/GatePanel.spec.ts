import { mount, RouterLinkStub } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import type { GateResult, GateStep, TaskProof, Turn, VerifierResult } from '@/api/types'
import { setupApp } from '@/test/app'
import { iso, makeDetail, makeTask } from '@/test/fixtures'

import { fixTurnsOf, gateOf, gateTone, gateUrgent, hasGate, stepCommand, stepNote } from './gate'
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
  autoFixes: 1,
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
    expect(wrapper.find('summary').text()).toBe('Gatefailedturn 32 automatic fixes')
    const text = wrapper.text()
    expect(text).toContain('Steps after turn 3')
    expect(text).toContain('cd web && npm test')
    expect(text).toContain('exit 1')
    expect(text).toContain('Not run: build')
    // Output is text: the markup is shown, never rendered.
    expect(wrapper.find('pre').text()).toBe('FAIL <b>x</b>')
    expect(wrapper.find('pre b').exists()).toBe(false)
    expect(text).toContain('src/')
    expect(text).toContain('claimed over t7')
    expect(text).toContain('1 of 2 used')
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
