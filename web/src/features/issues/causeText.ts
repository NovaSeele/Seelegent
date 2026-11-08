/**
 * A failed GitHub or git step in the reader's language (F14, D18): the daemon gives a cause code next to its English
 * error text, and the sentence comes from the code. The English text stays available behind "Seelegent said:".
 */
import type { OutboxEntry } from '@/api/types'

export type Cause = NonNullable<OutboxEntry['cause']>

/** Each cause to its key under issues.cause. */
const CAUSE_KEY: Readonly<Record<Cause, string>> = {
  git_auth: 'gitAuth',
  git_rejected: 'gitRejected',
  git: 'git',
  network: 'network',
  github_auth: 'githubAuth',
  github_rate_limited: 'githubRateLimited',
  github_forbidden: 'githubForbidden',
  github_not_found: 'githubNotFound',
  github_manual: 'githubManual',
  github: 'github',
  other: 'other',
}

/** The key of a cause's sentence; an unknown or missing cause reads as "other". */
export function causeKey(cause: string | undefined): string {
  return `issues.cause.${CAUSE_KEY[cause as Cause] ?? 'other'}`
}
