import { describe, expect, it } from 'vitest'

import { iso, makeAccount, T0 } from '@/test/fixtures'

import { accountState } from './accountState'

describe('account state', () => {
  it('maps the pool status first, then the runtime', () => {
    const cases: [Parameters<typeof makeAccount>[0], string][] = [
      [{ status: 'disabled' }, 'disabled'],
      [{ status: 'twin', twinOf: 'a0' }, 'twin'],
      [{ status: 'no_key' }, 'noKey'],
      [{ status: 'no_plan' }, 'noPlan'],
      [{ status: 'needs_login' }, 'needsLogin'],
      [{ status: 'needs_login', signingIn: true }, 'signingIn'],
      [{ status: 'new' }, 'checking'],
      [{ quota: { source: 'active', groups: [], exhaustedUntil: iso(60_000) } }, 'exhausted'],
      [{ quota: { source: 'active', groups: [], exhaustedUntil: iso(-60_000) } }, 'ready'],
      [{ resting: true }, 'resting'],
      [{ busy: 1 }, 'busy'],
      [{ status: 'error' }, 'ready'],
      [{}, 'ready'],
    ]
    for (const [overrides, state] of cases) {
      expect(accountState(makeAccount(overrides), T0), JSON.stringify(overrides)).toBe(state)
    }
  })
})
