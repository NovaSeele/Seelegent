import { describe, expect, it } from 'vitest'

import { activateLocale, i18n } from '@/i18n'

import { keyTestText } from './keyTest'

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
