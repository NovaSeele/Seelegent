import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick, ref } from 'vue'

import { ApiError, type ProofApi } from '@/api'
import type { LanesView, ProofIndex, ProofMatrix, ProofRun, ProofStats } from '@/api/types'

import { useProof } from './useProof'

const flush = async () => {
  for (let i = 0; i < 5; i++) await nextTick()
}

const runOf = (state: ProofRun['state']): ProofRun => ({
  id: 'r1',
  repoRoot: 'D:/app',
  state,
  startedAt: '2026-11-06T10:00:00Z',
  counts: { green: 0, red: 0, error: 0 },
  total: 1,
  results: [],
})

const index = (runs: ProofRun[] = []): ProofIndex =>
  ({ repoRoot: 'D:/app', features: [], runs }) as unknown as ProofIndex

function fakeApi(overrides: Partial<ProofApi> = {}): ProofApi {
  const missing = () => Promise.reject(new Error('unexpected call'))
  return {
    index: vi.fn(() => Promise.resolve(index())),
    matrix: vi.fn(() => Promise.resolve({ feature: 'specs/001-x' } as unknown as ProofMatrix)),
    run: vi.fn(missing),
    runStatus: vi.fn(missing),
    cancelRun: vi.fn(missing),
    stats: vi.fn(() => Promise.resolve({ tasks: 0 } as unknown as ProofStats)),
    lanes: vi.fn(() =>
      Promise.resolve({ repoRoot: 'D:/app', mode: 'warn', claims: [] } as LanesView),
    ),
    contention: vi.fn(missing),
    ...overrides,
  }
}

beforeEach(() => setActivePinia(createPinia()))

describe('useProof', () => {
  it('loads the index, stats and lanes of the repository and the matrix of the feature', async () => {
    const api = fakeApi()
    const cwd = ref('D:/app')
    const feature = ref('')
    const scope = effectScope()
    const proof = scope.run(() => useProof(cwd, feature, api))!
    await flush()
    expect(proof.state.value).toBe('ready')
    expect(proof.lanes.value?.mode).toBe('warn')
    expect(api.matrix).not.toHaveBeenCalled()
    feature.value = 'specs/001-x'
    await flush()
    expect(api.matrix).toHaveBeenCalledWith('D:/app', 'specs/001-x')
    expect(proof.matrix.value?.feature).toBe('specs/001-x')
    scope.stop()
  })

  it('reports the active tasks that refused a run, and reloads when a started run ends', async () => {
    const run = vi
      .fn<ProofApi['run']>()
      .mockRejectedValueOnce(
        new ApiError(409, 'conflict', 'tasks are active', {
          reason: 'active_tasks',
          activeTaskIds: ['t1', 't2'],
        }),
      )
      .mockResolvedValueOnce(runOf('running'))
    const api = fakeApi({ run, cancelRun: vi.fn(() => Promise.resolve(runOf('cancelled'))) })
    const scope = effectScope()
    const proof = scope.run(() => useProof(ref('D:/app'), ref(''), api))!
    await flush()
    expect(await proof.startRun(false)).toEqual({ active: ['t1', 't2'] })
    expect(await proof.startRun(true)).toBeNull()
    expect(run).toHaveBeenLastCalledWith({ cwd: 'D:/app', feature: undefined, force: true })
    expect(proof.index.value?.runs.map((r) => r.state)).toEqual(['running'])
    expect(api.index).toHaveBeenCalledTimes(1)
    await proof.cancelRun('r1')
    await flush()
    expect(api.cancelRun).toHaveBeenCalledWith('D:/app', 'r1')
    // The ended run changed the ledger: the index is read again.
    expect(api.index).toHaveBeenCalledTimes(2)
    scope.stop()
  })

  it('shows a failed read as an error', async () => {
    const api = fakeApi({ index: vi.fn(() => Promise.reject(new Error('boom'))) })
    const scope = effectScope()
    const proof = scope.run(() => useProof(ref('D:/app'), ref(''), api))!
    await flush()
    expect(proof.state.value).toBe('error')
    expect(proof.error.value).toContain('boom')
    scope.stop()
  })
})
