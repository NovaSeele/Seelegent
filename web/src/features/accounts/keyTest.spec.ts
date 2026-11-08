import { describe, expect, it } from 'vitest'

import { activateLocale, i18n } from '@/i18n'

import { keyTestMessage, keyTestText, testAllowsSave } from './keyTest'

describe('keyTestText', () => {
  it('words a pass, and says when the test could not see the credit balance', async () => {
    await activateLocale('en')
    const t = i18n.global.t as never
    expect(keyTestText({ reason: 'ok' }, t)).toEqual({ label: 'Key works', notes: [] })
    expect(keyTestText({ reason: 'ok', creditUnseen: true }, t)).toEqual({
      label: 'Key works',
      notes: ['This test cannot see the credit balance'],
    })
  })
})

describe('keyTestMessage', () => {
  it('says what happened and points at the fix', async () => {
    await activateLocale('en')
    const t = i18n.global.t as never
    const base = { status: 200, models: ['a', 'b'] }
    expect(keyTestMessage({ ...base, reason: 'ok' }, t)).toBe('Key works. 2 models available.')
    expect(keyTestMessage({ ...base, reason: 'ok', models: ['a'] }, t)).toBe(
      'Key works. 1 model available.',
    )
    expect(keyTestMessage({ ...base, reason: 'ok', creditUnseen: true }, t)).toBe(
      'Key works. 2 models available. This test cannot see the credit balance',
    )
    expect(keyTestMessage({ ...base, status: 404, reason: 'not_found' }, t)).toContain(
      'Remove /v1 from the end of the base URL',
    )
    expect(keyTestMessage({ ...base, status: 500, reason: 'error' }, t)).toBe(
      'The service answered with an error (HTTP 500).',
    )
  })
})

describe('testAllowsSave', () => {
  it('lets a pass or an uncheckable service through, nothing else', () => {
    expect(testAllowsSave({ ok: true, reason: 'ok' })).toBe(true)
    expect(testAllowsSave({ ok: false, reason: 'no_models_endpoint' })).toBe(true)
    for (const reason of ['refused', 'not_found', 'unreachable', 'redirected', 'error'] as const)
      expect(testAllowsSave({ ok: false, reason })).toBe(false)
  })
})
