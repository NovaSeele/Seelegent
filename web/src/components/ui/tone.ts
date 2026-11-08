import type { IconName } from './icons'

/** Colour roles a component can take (see styles/tokens.css and styles/tones.css). */
export type Tone = 'neutral' | 'accent' | 'running' | 'attention' | 'success' | 'danger' | 'muted'

/**
 * Task display states (old dashboard taskDisplayStatus). Each maps to one tone and one icon shape, so status is
 * never conveyed by colour alone. Labels come from common.status.<key>.
 */
export const TASK_STATUS = {
  queued: { tone: 'neutral', icon: 'statusQueued' },
  running: { tone: 'running', icon: 'statusRunning' },
  waiting: { tone: 'neutral', icon: 'statusWaiting' },
  waitingWorker: { tone: 'neutral', icon: 'statusWaiting' },
  /** waiting_quota with waiting "memory": the machine is short of free memory, not the account of quota. */
  waitingResources: { tone: 'neutral', icon: 'statusWaiting' },
  blocked: { tone: 'attention', icon: 'statusBlocked' },
  review: { tone: 'attention', icon: 'statusReview' },
  accepted: { tone: 'success', icon: 'statusAccepted' },
  rejected: { tone: 'danger', icon: 'statusRejected' },
  failed: { tone: 'danger', icon: 'statusFailed' },
  cancelled: { tone: 'muted', icon: 'statusCancelled' },
  waitingDeps: { tone: 'neutral', icon: 'statusWaiting' },
  blockedDeps: { tone: 'attention', icon: 'statusBlocked' },
} as const satisfies Record<string, { tone: Tone; icon: IconName }>

export type TaskStatus = keyof typeof TASK_STATUS
