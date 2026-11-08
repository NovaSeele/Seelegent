import { beforeEach, describe, expect, it } from 'vitest'

import type { Attention } from '@/api/types'
import { activateLocale, i18n, type Locale } from '@/i18n'

import { attentionText, depsText, inSentence } from './attentionText'

const at = (a: Partial<Attention> & Pick<Attention, 'reason'>): Attention =>
  ({ since: '', ...a }) as Attention

async function text(a: Attention, locale: Locale = 'en') {
  await activateLocale(locale)
  const { t, te } = i18n.global
  return attentionText(a, t as never, te as never)
}

describe('attention text (F14)', () => {
  beforeEach(async () => {
    await activateLocale('en')
  })

  it('builds the sentence from params, not from the daemon detail', async () => {
    const view = await text(
      at({
        reason: 'looping',
        detail: 'repeated run_shell 5 times in the last 8 steps',
        params: { kind: 'repeated', tool: 'run_shell', count: 5, steps: 8 },
      }),
    )
    expect(view.title).toBe(i18n.global.t('common.attention.looping'))
    expect(view.why).toBe(
      'The worker repeated the same run_shell call 5 times in its last 8 steps.',
    )
    expect(view.said).toBe('')
  })

  it('names a failed dependency by title and id', async () => {
    const view = await text(
      at({
        reason: 'deps_blocked',
        params: { kind: 'rejected', task: 't-1', title: 'Add login' },
      }),
    )
    expect(view.why).toBe('The earlier task Add login (t-1) was rejected.')
  })

  it('words how the worker ended from its code (design review)', async () => {
    const failed = (params?: Attention['params']) =>
      at({ reason: 'failed', detail: 'agy exited with code 0 and no result', params })
    const view = await text(failed({ kind: 'no_result', backend: 'agy', exitCode: 0 }))
    expect(view.why).toBe(
      'agy ended its turn (exit code 0) without sending a result. The task failed. Look at the last turn, ' +
        'then follow up or start it again.',
    )
    expect(view.said).toBe('')
    expect(
      (await text(failed({ kind: 'no_result', backend: 'agy', exitCode: 0 }), 'ja')).why,
    ).toContain('終了コード 0')
    // An error that is not how the process ended stays as the daemon said it.
    const other = await text(failed())
    expect(other.said).toBe('agy exited with code 0 and no result')
  })

  it('does not stop twice after a title that is a sentence (design review)', async () => {
    const view = await text(
      at({
        reason: 'deps_blocked',
        params: { kind: 'failed', task: 't-1', title: 'Migrate the cart to TypeScript.' },
      }),
    )
    expect(view.why).toBe('The earlier task Migrate the cart to TypeScript (t-1) failed.')
    expect(inSentence('Wait for it...')).toBe('Wait for it...')
    expect(inSentence('カートを移行する。')).toBe('カートを移行する')
  })

  it('counts with plural forms', async () => {
    expect((await text(at({ reason: 'work_at_risk', params: { count: 1 } }))).why).toBe(
      '1 file may have lost changes',
    )
    expect((await text(at({ reason: 'work_at_risk', params: { count: 3 } }))).why).toBe(
      '3 files may have lost changes',
    )
  })

  it('lists refused actions of a turn without a report', async () => {
    const view = await text(
      at({ reason: 'no_response', params: { denied: ['git push', 'rm -rf x'] } }),
    )
    expect(view.why).toContain('git push, rm -rf x')
  })

  it('keeps the daemon detail behind the disclosure when there are no params', async () => {
    const view = await text(at({ reason: 'stalled', detail: 'no output for 12m' }))
    expect(view.why).toBe('')
    expect(view.said).toBe('no output for 12m')
  })

  it('shows a failure as a sentence with the daemon error behind the disclosure', async () => {
    const view = await text(at({ reason: 'failed', detail: 'exit status 1' }))
    expect(view.why).toContain('The task failed.')
    expect(view.said).toBe('exit status 1')
  })

  it('quotes a verifier reason as it is', async () => {
    const view = await text(at({ reason: 'verifier_rejected', detail: 'tests do not cover x' }))
    expect(view.quote).toBe('tests do not cover x')
  })

  it('reads an unknown reason as a generic title and keeps the code', async () => {
    const view = await text(at({ reason: 'brand_new_reason' as Attention['reason'] }))
    expect(view.title).toBe('Needs a look')
    expect(view.said).toBe('brand_new_reason')
  })

  it('speaks the reader language', async () => {
    const view = await text(
      at({
        reason: 'stalled',
        detail: 'no output for 7m',
        params: { kind: 'no_output', minutes: 7 },
      }),
      'vi',
    )
    expect(view.why).toBe('Không có đầu ra trong 7 phút.')
    expect(view.why).not.toContain('no output')
  })
})

describe('dependency text (F14)', () => {
  const deps = async (task: Parameters<typeof depsText>[0], locale: Locale = 'en') => {
    await activateLocale(locale)
    const { t, te } = i18n.global
    return depsText(task, t as never, te as never)
  }

  it('says why a blocked task cannot start, never the daemon reason', async () => {
    const line = await deps({
      status: 'blocked_deps',
      after: ['t-1'],
      attention: at({
        reason: 'deps_blocked',
        detail: 'prerequisite t-1 (Add login) was failed',
        params: { kind: 'failed', task: 't-1', title: 'Add login' },
      }),
    })
    expect(line).toBe('The earlier task Add login (t-1) failed.')
  })

  it('names the tasks a waiting task waits for, in the reader language', async () => {
    const task: Parameters<typeof depsText>[0] = {
      status: 'waiting_deps',
      after: ['t-1', 't-2'],
      attention: null,
    }
    expect(await deps(task)).toBe('Waits for t-1, t-2 to finish.')
    expect(await deps(task, 'vi')).toBe('Đang chờ t-1, t-2 hoàn tất.')
  })

  it('has nothing to say for other tasks', async () => {
    expect(await deps({ status: 'running', after: ['t-1'], attention: null })).toBe('')
  })
})
