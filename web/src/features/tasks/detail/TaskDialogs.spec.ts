import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { shallowRef } from 'vue'

import type { Consult } from '@/api/types'
import { setupApp } from '@/test/app'
import { makeTask } from '@/test/fixtures'

import TaskDialogs, { type TaskDialog } from './TaskDialogs.vue'
import type { TaskActions } from './useTaskActions'

const objection: Consult = {
  id: 'c1',
  member: 'peri',
  rank: 'peer',
  status: 'done',
  verdict: 'object',
  summary: '',
  findings: [{ text: 'a discount over 100 makes the total negative', cites: [] }],
  required: ['reject discounts outside 0..100'],
  createdAt: '2026-11-08T00:00:00Z',
} as unknown as Consult

async function mountDialogs(review = vi.fn(() => Promise.resolve(true))) {
  const { plugins } = await setupApp()
  const actions = { busy: shallowRef(null), review } as unknown as TaskActions
  const wrapper = mount(TaskDialogs, {
    props: {
      modelValue: null as TaskDialog,
      task: makeTask({ id: 't1' }),
      actions,
      objections: [objection],
    },
    global: { plugins },
    attachTo: document.body,
  })
  const open = async (value: TaskDialog) => {
    await wrapper.setProps({ modelValue: value })
    await flushPromises()
  }
  return { wrapper, open, review }
}

const answerBox = () => document.body.querySelector<HTMLTextAreaElement>('textarea[required]')

afterEach(() => {
  document.body.innerHTML = ''
})

describe('TaskDialogs', () => {
  it('quotes what the objecting peer requires and keeps a typed answer when closed', async () => {
    const { wrapper, open, review } = await mountDialogs()
    const accept: TaskDialog = { kind: 'review', verdict: 'accepted' }
    await open(accept)
    expect(document.body.textContent).toContain('reject discounts outside 0..100')

    const box = answerBox()!
    box.value = 'Validated in the handler.'
    box.dispatchEvent(new Event('input'))
    await open(null) // Esc, the backdrop or Cancel
    await open(accept)
    expect(answerBox()!.value).toBe('Validated in the handler.')

    // Another dialog starts empty; so does the same one after it was sent.
    const button = [...document.body.querySelectorAll('button')].find((b) =>
      b.textContent?.includes('Accept'),
    )!
    button.click()
    await flushPromises()
    expect(review).toHaveBeenCalledWith('accepted', '', 'Validated in the handler.')
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([null])
    await open(null)
    await open(accept)
    expect(answerBox()!.value).toBe('')
  })
})
