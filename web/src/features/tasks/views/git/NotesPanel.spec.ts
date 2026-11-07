import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { computed, ref, shallowRef } from 'vue'

import type { Note } from '@/api/types'
import { flush, setupApp } from '@/test/app'

import NotesPanel from './NotesPanel.vue'
import { sendable, type NotesModel } from './useNotes'

const note = (overrides: Partial<Note>): Note => ({
  id: 'n1',
  taskId: 't1',
  file: 'b.go',
  line: 3,
  side: 'new',
  anchorText: 'x',
  body: 'Fix this',
  by: 'user',
  state: 'open',
  status: 'draft',
  anchorTurn: 1,
  createdAt: '2026-11-07T10:00:00.000Z',
  ...overrides,
})

function model(list: Note[], send = vi.fn(async () => 2)): NotesModel {
  const notes = shallowRef<readonly Note[]>(list)
  return {
    notes,
    toSend: computed(() => notes.value.filter(sendable)),
    status: ref('ready'),
    error: ref(''),
    reload: vi.fn(async () => {}),
    add: vi.fn(async () => {}),
    setState: vi.fn(async () => {}),
    remove: vi.fn(async () => {}),
    send,
  } as unknown as NotesModel
}

describe('NotesPanel', () => {
  const list = [
    note({ id: 'n2', file: 'b.go', line: 9, status: 'unaddressed', state: 'sent' }),
    note({ id: 'n1', file: 'a.go', line: 4 }),
    note({ id: 'n3', file: 'b.go', line: 2, status: 'resolved', state: 'resolved' }),
  ]

  it('lists notes by file and line and counts only what the next send includes', async () => {
    const { plugins } = await setupApp()
    const wrapper = mount(NotesPanel, {
      props: { notes: model(list), busy: false },
      global: { plugins },
    })
    expect(wrapper.findAll('.note__where').map((b) => b.text())).toEqual([
      'a.go:4',
      'b.go:2',
      'b.go:9',
    ])
    const send = wrapper.find('button[type="submit"]')
    expect(send.text()).toBe('Send 2 notes')
    expect(send.attributes('disabled')).toBeUndefined()
  })

  it('cannot send while the worker turn runs', async () => {
    const { plugins } = await setupApp()
    const wrapper = mount(NotesPanel, {
      props: { notes: model(list), busy: true },
      global: { plugins },
    })
    expect(wrapper.text()).toContain("Notes can be sent when the worker's turn has ended.")
    expect(wrapper.find('button[type="submit"]').attributes('disabled')).toBeDefined()
  })

  it('sends with the optional message and clears it', async () => {
    const { plugins } = await setupApp()
    const send = vi.fn(async () => 2)
    const wrapper = mount(NotesPanel, {
      props: { notes: model(list, send), busy: false },
      global: { plugins },
    })
    await wrapper.find('textarea').setValue('Then run the tests.')
    await wrapper.find('form').trigger('submit')
    await flush()
    expect(send).toHaveBeenCalledWith('Then run the tests.')
    expect((wrapper.find('textarea').element as HTMLTextAreaElement).value).toBe('')
  })

  it('opens the file of a note', async () => {
    const { plugins } = await setupApp()
    const wrapper = mount(NotesPanel, {
      props: { notes: model(list), busy: false },
      global: { plugins },
    })
    await wrapper.find('.note__where').trigger('click')
    expect(wrapper.emitted('open')).toEqual([['a.go']])
  })
})
