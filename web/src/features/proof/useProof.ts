/**
 * The data of the Proof page for one repository: the index (features, totals, latest runs), the matrix of the
 * selected feature, the lane claims and the gate statistics. Regression runs update live over proof.run; when a
 * run ends, the index and the matrix are read again (the ledger changed). Everything is read again after a resync.
 */
import { onScopeDispose, shallowRef, watch, type Ref } from 'vue'

import { ApiError, errorMessage, proofApi, type ProofApi } from '@/api'
import type { LanesView, ProofIndex, ProofMatrix, ProofRun, ProofStats } from '@/api/types'
import { useConnectionStore } from '@/stores/connection'

import { withRun } from './proofModel'

export type LoadState = 'idle' | 'loading' | 'ready' | 'error'

export function useProof(cwd: Ref<string>, feature: Ref<string>, api: ProofApi = proofApi) {
  const state = shallowRef<LoadState>('idle')
  const error = shallowRef('')
  const index = shallowRef<ProofIndex | null>(null)
  const stats = shallowRef<ProofStats | null>(null)
  const lanes = shallowRef<LanesView | null>(null)
  const matrix = shallowRef<ProofMatrix | null>(null)
  const matrixError = shallowRef('')
  const matrixLoading = shallowRef(false)
  /** Set while a run is being started or cancelled. */
  const busy = shallowRef(false)

  let loadSeq = 0
  async function load() {
    const dir = cwd.value
    const seq = ++loadSeq
    if (!dir) {
      state.value = 'idle'
      index.value = stats.value = lanes.value = null
      return
    }
    if (!index.value) state.value = 'loading'
    try {
      const [ix, st, ln] = await Promise.all([api.index(dir), api.stats(dir), api.lanes(dir)])
      if (seq !== loadSeq) return
      index.value = ix
      stats.value = st
      lanes.value = ln
      error.value = ''
      state.value = 'ready'
    } catch (e) {
      if (seq !== loadSeq) return
      error.value = errorMessage(e)
      state.value = 'error'
    }
  }

  let matrixSeq = 0
  async function loadMatrix() {
    const dir = cwd.value
    const f = feature.value
    const seq = ++matrixSeq
    if (!dir || !f) {
      matrix.value = null
      matrixError.value = ''
      return
    }
    matrixLoading.value = true
    try {
      const m = await api.matrix(dir, f)
      if (seq !== matrixSeq) return
      matrix.value = m
      matrixError.value = ''
    } catch (e) {
      if (seq !== matrixSeq) return
      matrix.value = null
      matrixError.value = errorMessage(e)
    } finally {
      if (seq === matrixSeq) matrixLoading.value = false
    }
  }

  watch(
    cwd,
    () => {
      index.value = stats.value = lanes.value = null
      void load()
    },
    { immediate: true },
  )
  watch([cwd, feature], () => void loadMatrix(), { immediate: true })

  function applyRun(run: ProofRun) {
    if (!index.value) return
    const before = index.value.runs.find((r) => r.id === run.id)
    const next = withRun(index.value, run)
    if (next === index.value) return
    index.value = next
    // A run that ended (seen running or not) changed the ledger: read the index and the matrix again.
    if (run.state !== 'running' && before?.state !== run.state) {
      void load()
      void loadMatrix()
    }
  }

  /** Starts a regression run; returns the conflict's active task ids when tasks of the repository are busy. */
  async function startRun(
    force: boolean,
    onlyFeature?: string,
  ): Promise<{ active?: string[] } | null> {
    busy.value = true
    try {
      const run = await api.run({ cwd: cwd.value, feature: onlyFeature || undefined, force })
      applyRun(run)
      return null
    } catch (e) {
      if (e instanceof ApiError && e.details.reason === 'active_tasks') {
        return { active: (e.details.activeTaskIds ?? []) as string[] }
      }
      throw e
    } finally {
      busy.value = false
    }
  }

  /** Reads a run's results (the index lists runs without them); a failed read leaves the run as it is. */
  async function loadRun(id: string) {
    try {
      applyRun(await api.runStatus(cwd.value, id))
    } catch {
      // The summary stays; the next open tries again.
    }
  }

  async function cancelRun(id: string) {
    busy.value = true
    try {
      applyRun(await api.cancelRun(cwd.value, id))
    } finally {
      busy.value = false
    }
  }

  const connection = useConnectionStore()
  const stopRuns = connection.subscribe('proof.run', applyRun)
  const stopResync = connection.onResync(() => {
    void load()
    void loadMatrix()
  })
  onScopeDispose(() => {
    stopRuns()
    stopResync()
  })

  return {
    state,
    error,
    index,
    stats,
    lanes,
    matrix,
    matrixError,
    matrixLoading,
    busy,
    load,
    startRun,
    cancelRun,
    loadRun,
  }
}
