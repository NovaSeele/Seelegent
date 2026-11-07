/** Events a hook can run on (the daemon's hook events, as in the old hooks.json). */
export const HOOK_EVENTS = [
  'task.created',
  'task.started',
  'task.blocked',
  'task.unblocked',
  'task.done',
  'task.failed',
  'task.reviewed',
  'task.cancelled',
  'task.takeover',
  'task.handback',
  'account.exhausted',
  'account.needs_login',
  'daemon.started',
  'daemon.reattached',
] as const
