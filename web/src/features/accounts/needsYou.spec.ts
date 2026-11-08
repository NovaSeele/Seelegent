import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import type { AccountsApi } from '@/api/accounts'
import type { ProvidersApi } from '@/api/providers'
import type { Account } from '@/api/types'
import { useAccountsStore } from '@/stores/accounts'
import { useProvidersStore } from '@/stores/providers'
import { setupApp } from '@/test/app'
import { makeAccount } from '@/test/fixtures'

import NeedsYou from './NeedsYou.vue'
import { needsYou } from './needsYou'

const key = (overrides: Parameters<typeof makeAccount>[0] = {}) =>
  makeAccount({
    kind: 'api-key',
    backend: 'claude',
    provider: 'deepseek',
    status: 'ok',
    ...overrides,
  })

describe('needsYou', () => {
  it('lists each account that waits for the user, with the step that fixes it', () => {
    const rows = needsYou([
      key({ id: 'a', status: 'no_key' }),
      makeAccount({ id: 'b', kind: 'isolated', status: 'needs_login' }),
      key({ id: 'c', testResult: 'refused' }),
      key({ id: 'd', testResult: 'not_found' }),
      key({ id: 'e', restReason: 'credits' }),
      makeAccount({ id: 'f', kind: 'isolated', restReason: 'credits' }),
      key({ id: 'g', restReason: 'budget_reached' }),
      makeAccount({ id: 'h', kind: 'isolated', status: 'no_plan' }),
      key({ id: 'ok', testResult: 'ok' }),
      key({ id: 'slow', testResult: 'unreachable' }),
    ])
    expect(rows.map((r) => [r.account.id, r.problem, r.action])).toEqual([
      ['a', 'no_key', 'key'],
      ['b', 'needs_login', 'login'],
      ['c', 'refused', 'replaceKey'],
      ['d', 'refused', 'replaceKey'],
      ['e', 'credits', 'test'],
      ['f', 'credits', null],
      ['g', 'budget_reached', 'budget'],
      ['h', 'no_plan', null],
    ])
  })

  it('leaves out accounts switched off or signing in', () => {
    expect(
      needsYou([
        key({ status: 'no_key', enabled: false }),
        makeAccount({ kind: 'isolated', status: 'needs_login', signingIn: true }),
      ]),
    ).toEqual([])
  })

  it('names one problem per account: the missing key before anything else', () => {
    const rows = needsYou([
      key({ status: 'no_key', restReason: 'budget_reached', testResult: 'refused' }),
    ])
    expect(rows).toHaveLength(1)
    expect(rows[0]?.problem).toBe('no_key')
  })
})

describe('NeedsYou', () => {
  it('says each problem in a sentence and runs the step that fixes it', async () => {
    const { plugins } = await setupApp()
    const login = vi.fn(() => Promise.reject(new Error('not now')))
    useAccountsStore().setApi({ login } as unknown as AccountsApi)
    const test = vi.fn(() =>
      Promise.resolve({
        ok: true,
        status: 200,
        reason: 'ok',
        message: '',
        models: [],
        latencyMs: 1,
      }),
    )
    useProvidersStore().setApi({ test } as unknown as ProvidersApi)
    const accounts = [
      key({ id: 'a', label: 'Claude Code 2', status: 'no_key' }),
      makeAccount({ id: 'b', label: 'Gemini 1', kind: 'isolated', status: 'needs_login' }),
      key({ id: 'c', label: 'DeepSeek 1', testResult: 'not_found' }),
      key({ id: 'd', label: 'OpenRouter 1', restReason: 'credits' }),
      key({
        id: 'e',
        label: 'Anthropic 1',
        restReason: 'budget_reached',
        spentUsd: 200.5,
        budgetUsd: 200,
      }),
    ]
    const w = mount(NeedsYou, { props: { needs: needsYou(accounts) }, global: { plugins } })
    const rows = w.findAll('.needs__row')
    expect(rows.map((r) => r.find('.needs__text').text())).toEqual([
      'Claude Code 2: no key.',
      'Gemini 1: signed out.',
      "DeepSeek 1: the service's address looks wrong.",
      'OpenRouter 1: no credit left.',
      'Anthropic 1: budget reached ($200.50 of $200).',
    ])
    expect(rows.map((r) => r.find('button').text())).toEqual([
      'Add key',
      'Sign in',
      'Replace key',
      'Test key',
      'Raise budget',
    ])

    await rows[0]?.find('button').trigger('click')
    await rows[2]?.find('button').trigger('click')
    await rows[4]?.find('button').trigger('click')
    expect(w.emitted<[Account, string]>('dialog')?.map(([a, kind]) => [a.id, kind])).toEqual([
      ['a', 'key'],
      ['c', 'key'],
      ['e', 'budget'],
    ])
    await rows[1]?.find('button').trigger('click')
    await rows[3]?.find('button').trigger('click')
    await flushPromises()
    expect(login).toHaveBeenCalledWith('b')
    expect(test).toHaveBeenCalledWith({ accountId: 'd' })
  })
})
