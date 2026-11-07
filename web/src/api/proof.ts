/** Typed endpoints of the proof ledger, regression runs, lane claims and gate statistics (contract §6, M13). */
import type { ApiClient } from './client'
import type {
  ContentionView,
  LanesView,
  ProofIndex,
  ProofMatrix,
  ProofRun,
  ProofRunRequest,
  ProofStats,
} from './types'

const enc = encodeURIComponent

export function createProofApi(client: ApiClient) {
  return {
    index: (cwd: string, signal?: AbortSignal) =>
      client.get<ProofIndex>('/proof/index', { signal, query: { cwd } }),
    /** `feature`: the folder as the index lists it (relative to the repository root, or absolute). */
    matrix: (cwd: string, feature: string, signal?: AbortSignal) =>
      client.get<ProofMatrix>('/proof/matrix', { signal, query: { cwd, feature } }),
    /** 202 with the started run; 409 while other tasks of the repository are active (unless force). */
    run: (body: ProofRunRequest) => client.post<ProofRun>('/proof/run', { body }),
    runStatus: (cwd: string, id: string) =>
      client.get<ProofRun>(`/proof/runs/${enc(id)}`, { query: { cwd } }),
    cancelRun: (cwd: string, id: string) =>
      client.post<ProofRun>(`/proof/runs/${enc(id)}/cancel`, { body: { cwd } }),
    stats: (cwd: string, signal?: AbortSignal) =>
      client.get<ProofStats>('/proof/stats', { signal, query: { cwd } }),
    lanes: (cwd: string, signal?: AbortSignal) =>
      client.get<LanesView>('/lanes', { signal, query: { cwd } }),
    contention: (cwd: string, signal?: AbortSignal) =>
      client.get<ContentionView>('/contention', { signal, query: { cwd } }),
  }
}

export type ProofApi = ReturnType<typeof createProofApi>
