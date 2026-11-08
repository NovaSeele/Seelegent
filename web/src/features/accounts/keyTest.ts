import type { KeyTestResult } from '@/api/types'

export type Translate = (key: string, named?: Record<string, unknown>) => string

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
