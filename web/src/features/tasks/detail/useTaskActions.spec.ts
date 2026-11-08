import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'

import type { TasksApi } from '@/api'
import type { TaskSummary } from '@/api/types'
import type { Locale } from '@/i18n'
import { setupApp } from '@/test/app'
import { makeTask } from '@/test/fixtures'
import { DURATION_LONG, useToastStore } from '@/stores/toast'

import { useTaskActions, type TaskActions } from './useTaskActions'

async function withActions(summary: TaskSummary, locale?: Locale): Promise<TaskActions> {
  const { plugins } = await setupApp(locale)
  const api = { review: vi.fn(() => Promise.resolve(summary)) } as unknown as TasksApi
  let actions: TaskActions | null = null
  mount(
    defineComponent({
      setup() {
        actions = useTaskActions(() => 't1', api)
        return () => null
      },
    }),
    { global: { plugins } },
  )
  if (!actions) throw new Error('not mounted')
  return actions
}

describe('useTaskActions review', () => {
  it('names a senior objection in the acceptance toast and keeps it long enough to read', async () => {
    const actions = await withActions(
      makeTask({
        warnings: ['senior sam objected: the total can go negative', 'senior una objected'],
        objections: [{ member: 'sam', text: 'the total can go negative' }, { member: 'una' }],
      }),
    )
    expect(await actions.review('accepted')).toBe(true)
    const [toast] = useToastStore().toasts
    expect(toast?.message).toBe(
      'Accepted. Warning: sam objected: the total can go negative; una objected',
    )
    expect(toast?.tone).toBe('info')
    expect(toast?.duration).toBe(DURATION_LONG)
  })

  it("builds the toast in the reader's language, never from the daemon's English warnings (F14)", async () => {
    const actions = await withActions(
      makeTask({
        warnings: ['senior sam objected: x'],
        objections: [{ member: 'sam', text: 'x' }],
      }),
      'vi',
    )
    await actions.review('accepted')
    const [toast] = useToastStore().toasts
    expect(toast?.message).not.toContain('senior')
    expect(toast?.message).not.toContain('objected')
    expect(toast?.message).toContain('sam')
  })

  it('says only "Accepted" when nothing was objected', async () => {
    const actions = await withActions(makeTask())
    await actions.review('accepted')
    const [toast] = useToastStore().toasts
    expect(toast?.message).toBe('Accepted')
    expect(toast?.tone).toBe('success')
  })
})

// Old B21 (E-B21): the copy was not awaited, so a refused clipboard still showed "copied".
describe('useTaskActions copyId', () => {
  afterEach(() => {
    Reflect.deleteProperty(navigator, 'clipboard')
  })

  function stubClipboard(writeText: (text: string) => Promise<void>) {
    const clipboard = { writeText: vi.fn(writeText) }
    Object.defineProperty(navigator, 'clipboard', { value: clipboard, configurable: true })
    return clipboard.writeText
  }

  it('says copied only once the clipboard took the id', async () => {
    const writeText = stubClipboard(() => Promise.resolve())
    const actions = await withActions(makeTask())
    expect(await actions.copyId()).toBe(true)
    expect(writeText).toHaveBeenCalledWith('t1')
    const [toast] = useToastStore().toasts
    expect(toast).toMatchObject({ message: 'Task ID copied', tone: 'success' })
  })

  it('says it could not copy when the browser refuses, and never "copied"', async () => {
    stubClipboard(() => Promise.reject(new DOMException('denied', 'NotAllowedError')))
    const actions = await withActions(makeTask())
    expect(await actions.copyId()).toBe(false)
    const toasts = useToastStore().toasts
    expect(toasts.map((t) => t.message)).toEqual([
      'Could not copy: the browser refused clipboard access',
    ])
    expect(toasts[0]?.tone).toBe('error')
  })
})
