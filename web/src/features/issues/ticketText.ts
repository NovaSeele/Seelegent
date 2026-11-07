/**
 * The words of a ticket that come from the daemon as codes: why a push was refused and what to do about it, each
 * refused file with its next step, and the steps of the ticket's log. The daemon's English text (the lead's tools
 * show it) never reaches the page: everything here is an i18n key with its values.
 */
import type { ScanFinding, TicketLogEntry, TicketRefusal } from '@/api/types'

/** An i18n key with its values; n picks the plural form. */
export interface Words {
  key: string
  params: Record<string, string | number>
  n?: number
}

/** A refused file: its path, what it is and the step that fixes it. */
export interface FindingView {
  path: string
  what: Words
  fix: Words
}

/** A refused push: the title, what it means and the next step, with the files when there are any. */
export interface RefusalView {
  title: Words
  why: Words | null
  next: Words
  detail: string
  findings: FindingView[]
}

const SECRET_FILES: ReadonlySet<ScanFinding['kind']> = new Set([
  'env',
  'private_key',
  'key_store',
  'credentials',
  'secret',
])

const SECRETS: ReadonlySet<string> = new Set([
  'github_token',
  'aws_key',
  'private_key',
  'slack_token',
  'google_key',
  'anthropic_key',
  'stored_token',
])

/** What a refused file is and what to do: delete or ignore a key file, take a key out of the code, and so on. */
export function findingView(f: ScanFinding, megabytes: (bytes: number) => string): FindingView {
  const base = 'issues.finding'
  switch (f.kind) {
    case 'secret': {
      const secret = f.detail && SECRETS.has(f.detail) ? f.detail : 'unknown'
      return {
        path: f.path,
        what: { key: `${base}.secret.${secret}`, params: {} },
        fix: { key: `${base}.fix.secret`, params: {} },
      }
    }
    case 'large': {
      const bytes = Number(f.detail)
      return {
        path: f.path,
        what: {
          key:
            Number.isFinite(bytes) && bytes > 0 ? `${base}.kind.largeSize` : `${base}.kind.large`,
          params: { size: Number.isFinite(bytes) ? megabytes(bytes) : '' },
        },
        fix: { key: `${base}.fix.large`, params: {} },
      }
    }
    case 'key_store':
      return {
        path: f.path,
        what: { key: `${base}.kind.key_store`, params: { ext: f.detail ?? '' } },
        fix: { key: `${base}.fix.file`, params: {} },
      }
    case 'device':
    case 'link':
      return {
        path: f.path,
        what: { key: `${base}.kind.${f.kind}`, params: {} },
        fix: { key: `${base}.fix.${f.kind}`, params: {} },
      }
    default:
      return {
        path: f.path,
        what: { key: `${base}.kind.${f.kind}`, params: {} },
        fix: { key: `${base}.fix.file`, params: {} },
      }
  }
}

/** True when every finding is a secret or a key, so the title can say "secrets" instead of "files". */
export function allSecrets(findings: readonly ScanFinding[]): boolean {
  return findings.length > 0 && findings.every((f) => SECRET_FILES.has(f.kind))
}

/** A refused push in the reader's words: one frame for every reason, each with its own explanation and next step. */
export function refusalView(
  rf: Pick<TicketRefusal, 'reason' | 'findings' | 'branch' | 'commits' | 'cause' | 'detail'>,
  megabytes: (bytes: number) => string,
): RefusalView {
  const base = 'issues.refusal'
  const branch = rf.branch ?? ''
  const findings = (rf.findings ?? []).map((f) => findingView(f, megabytes))
  switch (rf.reason) {
    case 'unsafe_files': {
      const n = findings.length
      return {
        title: {
          key: allSecrets(rf.findings) ? `${base}.title.secrets` : `${base}.title.files`,
          params: { count: n },
          n,
        },
        why: { key: `${base}.why.unsafeFiles`, params: {} },
        next: { key: `${base}.next.unsafeFiles`, params: {} },
        detail: '',
        findings,
      }
    }
    case 'scan_failed':
      return rf.cause === 'changed'
        ? {
            title: { key: `${base}.title.scanChanged`, params: {} },
            why: { key: `${base}.why.scanChanged`, params: {} },
            next: { key: `${base}.next.scanChanged`, params: {} },
            detail: '',
            findings: [],
          }
        : {
            title: { key: `${base}.title.scanFailed`, params: {} },
            why: { key: `${base}.why.scanFailed`, params: {} },
            next: { key: `${base}.next.scanFailed`, params: {} },
            detail: rf.detail ?? '',
            findings: [],
          }
    case 'base_not_pushed': {
      const commits = rf.commits ?? 0
      return commits > 0
        ? {
            title: { key: `${base}.title.baseNotPushed`, params: { branch, commits }, n: commits },
            why: { key: `${base}.why.baseNotPushed`, params: { branch } },
            next: { key: `${base}.next.baseNotPushed`, params: { branch } },
            detail: '',
            findings: [],
          }
        : {
            title: { key: `${base}.title.baseMissing`, params: { branch } },
            why: null,
            next: { key: `${base}.next.baseNotPushed`, params: { branch } },
            detail: '',
            findings: [],
          }
    }
    default:
      return {
        title: { key: `${base}.title.branchDiverged`, params: { branch } },
        why: { key: `${base}.why.branchDiverged`, params: { branch } },
        next: { key: `${base}.next.branchDiverged`, params: { branch } },
        detail: '',
        findings: [],
      }
  }
}

/** A Go duration ("15h20m0s", "1m30.5s", "250ms") in milliseconds, or null when the text is not one. */
export function goDuration(text: string): number | null {
  const m = /^(?:(\d+)h)?(?:(\d+)m(?!s))?(?:(\d+(?:\.\d+)?)s)?(?:(\d+(?:\.\d+)?)ms)?$/.exec(text)
  if (!m || text === '' || m.slice(1).every((g) => g === undefined)) return null
  const [, h, min, s, ms] = m
  return (
    Number(h ?? 0) * 3_600_000 + Number(min ?? 0) * 60_000 + Number(s ?? 0) * 1000 + Number(ms ?? 0)
  )
}

const ISO_TIME = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}/

/** Formatters a log line needs: the page's locale formatting and its translations. */
export interface LogFormat {
  duration: (ms: number) => string
  dateTime: (iso: string) => string
  /** The translation of key, or null when the catalog has none. */
  word: (key: string) => string | null
}

/** One value of a log step's data as the reader reads it: times and durations formatted, known codes translated. */
function logValue(key: string, v: unknown, f: LogFormat): string {
  if (typeof v === 'string') {
    if (ISO_TIME.test(v)) return f.dateTime(v)
    const ms = goDuration(v)
    if (ms !== null) return f.duration(ms)
    const code =
      key === 'status'
        ? (f.word(`issues.ticket.status.${v}`) ?? f.word(`issues.ticket.outboxStatus.${v}`))
        : key === 'kind'
          ? f.word(`issues.ticket.outboxKind.${v}`)
          : key === 'reason'
            ? f.word(`issues.ticket.logReason.${v}`)
            : null
    return code ?? v
  }
  if (Array.isArray(v)) return String(v.length)
  return typeof v === 'object' && v !== null ? JSON.stringify(v) : String(v)
}

/** A log step: its translated name and a short line of its data. */
export function logLine(
  e: Pick<TicketLogEntry, 'type' | 'data'>,
  f: LogFormat,
  limit = 240,
): { label: string; text: string } {
  const label = f.word(`issues.ticket.logType.${e.type}`) ?? e.type
  const parts: string[] = []
  for (const [k, v] of Object.entries(e.data)) {
    if (v === null || v === undefined || v === '') continue
    const name = f.word(`issues.ticket.logKey.${k}`) ?? k
    parts.push(`${name}: ${logValue(k, v, f)}`)
  }
  const text = parts.join(' · ')
  return { label, text: text.length > limit ? `${text.slice(0, limit - 1)}…` : text }
}
