import type { KeyTestResult } from '@/api/types'

export type Translate = (key: string, named?: Record<string, unknown>, plural?: number) => string

/**
 * A key test's result in the reader's language: the result word, and the notes that go with it. A pass while the
 * account rested for lack of credit adds that the test cannot see the balance: it lists models, which works at
 * zero credit too (the pass still lifts the rest).
 */
export function keyTestText(
  result: Pick<KeyTestResult, 'reason' | 'creditUnseen'>,
  t: Translate,
): { label: string; notes: string[] } {
  const notes: string[] = []
  if (result.creditUnseen) notes.push(t('accounts.testResult.creditUnseen'))
  return { label: t(`accounts.testResult.${result.reason}`), notes }
}

/**
 * The sentence a form shows after a key test: what happened and the fix it points at ("Key works. 14 models
 * available.", "The service refused this key.", "Wrong address: … Remove /v1 from the end …"), plus the notes.
 */
export function keyTestMessage(
  result: Pick<KeyTestResult, 'reason' | 'creditUnseen' | 'models' | 'status'>,
  t: Translate,
): string {
  const n = result.models.length
  const main =
    result.reason === 'ok'
      ? t('accounts.testHelp.ok', { n }, n)
      : t(`accounts.testHelp.${result.reason}`, { status: result.status })
  return [main, ...keyTestText(result, t).notes].join(' ')
}

/** Can an account be added (or a key saved) after this test: a pass, or a service that cannot be checked. */
export const testAllowsSave = (result: Pick<KeyTestResult, 'ok' | 'reason'>): boolean =>
  result.ok || result.reason === 'no_models_endpoint'
