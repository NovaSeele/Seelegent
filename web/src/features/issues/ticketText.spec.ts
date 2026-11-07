import { describe, expect, it } from 'vitest'

import type { ScanFinding } from '@/api/types'

import { allSecrets, findingView, goDuration, logLine, refusalView } from './ticketText'

const mb = (bytes: number) => `${(bytes / 1_048_576).toFixed(1)} MB`
const finding = (path: string, kind: ScanFinding['kind'], detail?: string): ScanFinding => ({
  path,
  kind,
  detail,
  reason: 'English text the page never shows',
})

describe('ticketText', () => {
  it('gives each refused file its own next step', () => {
    expect(findingView(finding('.env', 'env'), mb).fix.key).toBe('issues.finding.fix.file')
    expect(findingView(finding('a.pem', 'key_store', '.pem'), mb).what).toEqual({
      key: 'issues.finding.kind.key_store',
      params: { ext: '.pem' },
    })
    const secret = findingView(finding('src/cfg.go', 'secret', 'aws_key'), mb)
    expect(secret.what.key).toBe('issues.finding.secret.aws_key')
    expect(secret.fix.key).toBe('issues.finding.fix.secret')
    expect(findingView(finding('x', 'secret', 'made_up'), mb).what.key).toBe(
      'issues.finding.secret.unknown',
    )
    expect(findingView(finding('big.bin', 'large', '7340032'), mb).what).toEqual({
      key: 'issues.finding.kind.largeSize',
      params: { size: '7.0 MB' },
    })
    expect(findingView(finding('nul', 'device'), mb).fix.key).toBe('issues.finding.fix.device')
    expect(findingView(finding('l', 'link'), mb).fix.key).toBe('issues.finding.fix.link')
  })

  it('frames every refusal reason with a title, an explanation and a next step', () => {
    const files = refusalView(
      {
        reason: 'unsafe_files',
        findings: [finding('.env', 'env'), finding('k.go', 'secret', 'github_token')],
      },
      mb,
    )
    expect(files.title).toEqual({ key: 'issues.refusal.title.secrets', params: { count: 2 }, n: 2 })
    expect(files.findings).toHaveLength(2)
    expect(
      refusalView({ reason: 'unsafe_files', findings: [finding('nul', 'device')] }, mb).title.key,
    ).toBe('issues.refusal.title.files')
    expect(
      refusalView({ reason: 'base_not_pushed', findings: [], branch: 'main', commits: 2 }, mb)
        .title,
    ).toEqual({
      key: 'issues.refusal.title.baseNotPushed',
      params: { branch: 'main', commits: 2 },
      n: 2,
    })
    expect(
      refusalView({ reason: 'base_not_pushed', findings: [], branch: 'main' }, mb).title.key,
    ).toBe('issues.refusal.title.baseMissing')
    const git = refusalView(
      { reason: 'scan_failed', findings: [], cause: 'git', detail: 'bad object' },
      mb,
    )
    expect([git.title.key, git.detail]).toEqual(['issues.refusal.title.scanFailed', 'bad object'])
    expect(
      refusalView({ reason: 'scan_failed', findings: [], cause: 'changed' }, mb).next.key,
    ).toBe('issues.refusal.next.scanChanged')
    expect(refusalView({ reason: 'branch_diverged', findings: [], branch: 'b' }, mb).next.key).toBe(
      'issues.refusal.next.branchDiverged',
    )
    // The commits the remote lacks, by subject (the why is plural with them), and how many more.
    const base = refusalView(
      {
        reason: 'base_not_pushed',
        findings: [],
        branch: 'main',
        commits: 5,
        subjects: ['a', 'b', 'c'],
      },
      mb,
    )
    expect([base.subjects, base.moreCommits, base.why?.n]).toEqual([['a', 'b', 'c'], 2, 5])
    // A compare link to the commits Seelegent did not push; only a web address is kept.
    const diverged = (u: string) =>
      refusalView({ reason: 'branch_diverged', findings: [], branch: 'b', compareUrl: u }, mb)
        .compareUrl
    expect(diverged('https://github.com/o/r/compare/abc...def')).toBe(
      'https://github.com/o/r/compare/abc...def',
    )
    expect(diverged('javascript:alert(1)')).toBe('')
    expect(allSecrets([])).toBe(false)
  })

  it('reads Go durations', () => {
    expect(goDuration('15h20m0s')).toBe(15 * 3_600_000 + 20 * 60_000)
    expect(goDuration('1m30.5s')).toBe(90_500)
    expect(goDuration('250ms')).toBe(250)
    expect(goDuration('')).toBeNull()
    expect(goDuration('main')).toBeNull()
    expect(goDuration('5m')).toBe(300_000)
  })

  it('writes a log step with translated names and formatted values', () => {
    const words: Record<string, string> = {
      'issues.ticket.logType.status': 'Status',
      'issues.ticket.status.ready': 'Ready for a PR',
      'issues.ticket.logKey.status': 'status',
    }
    const f = {
      duration: (ms: number) => `${ms / 60_000} min`,
      dateTime: () => 'Nov 7, 10:00',
      word: (k: string) => words[k] ?? null,
    }
    expect(logLine({ type: 'status', data: { status: 'ready' } }, f)).toEqual({
      label: 'Status',
      text: 'Ready for a PR',
    })
    words['issues.ticket.phase.open_pr'] = 'Branch pushed: open the PR'
    words['issues.ticket.outboxKind.open_pr'] = 'Open pull request'
    words['issues.ticket.outboxStatus.linkReady'] = 'Link ready'
    words['issues.ticket.outboxStatus.failed'] = 'Failed'
    words['issues.ticket.logKey.error'] = 'error'
    words['issues.ticket.outboxKind.push'] = 'Push'
    words['issues.ticket.outboxStatus.refused'] = 'Refused'
    expect(
      logLine({ type: 'outbox', data: { id: 5, kind: 'push', status: 'failed', refused: true } }, f)
        .text,
    ).toBe('Push: Refused')
    // The same words as the header and the outbox list; no row numbers.
    expect(logLine({ type: 'status', data: { status: 'pr_open', phase: 'open_pr' } }, f).text).toBe(
      'Branch pushed: open the PR',
    )
    expect(
      logLine({ type: 'outbox', data: { id: 3, kind: 'open_pr', status: 'done', manual: true } }, f)
        .text,
    ).toBe('Open pull request: Link ready')
    expect(
      logLine({ type: 'outbox', data: { id: 4, kind: 'open_pr', status: 'failed', error: 'x' } }, f)
        .text,
    ).toBe('Open pull request: Failed · error: x')
    expect(
      logLine({ type: 'odd', data: { wait: '5m', at: '2026-11-07T10:00:00Z', n: [1, 2] } }, f),
    ).toEqual({ label: 'odd', text: 'wait: 5 min · at: Nov 7, 10:00 · n: 2' })
    expect(logLine({ type: 'x', data: { text: 'y'.repeat(300) } }, f, 20).text).toHaveLength(20)
  })
})
