import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { SettingsApi } from '@/api/settings'
import type { Memory, Project } from '@/api/types'
import { useConnectionStore } from '@/stores/connection'
import { useSettingsStore } from '@/stores/settings'
import { setupApp } from '@/test/app'

import MemorySection from './MemorySection.vue'

const project = (cwd: string, name: string): Project => ({
  key: name,
  cwd,
  name,
  taskCount: 1,
  activeCount: 0,
  lastActivityAt: '2026-11-01T00:00:00Z',
})

async function mountSection() {
  const { plugins } = await setupApp()
  useConnectionStore().projects = [project('D:/shop', 'shop'), project('D:/blog', 'blog')]
  const memory = vi.fn((cwd: string) => Promise.resolve<Memory>({ cwd, text: `notes of ${cwd}` }))
  useSettingsStore().setApi({ memory } as unknown as SettingsApi)
  const wrapper = mount(MemorySection, { global: { plugins }, attachTo: document.body })
  await flushPromises()
  return { wrapper, memory }
}

function dialogButton(label: string): HTMLButtonElement {
  const found = [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(
    (b) => b.textContent?.trim() === label,
  )
  if (!found) throw new Error(`no button "${label}"`)
  return found
}

afterEach(() => {
  document.body.innerHTML = ''
})

describe('MemorySection', () => {
  it('asks before a project switch drops unsaved edits', async () => {
    const { wrapper, memory } = await mountSection()
    const textarea = wrapper.get('textarea')
    expect((textarea.element as HTMLTextAreaElement).value).toBe('notes of D:/shop')
    await textarea.setValue('my edit')
    const select = wrapper.get('select')
    await select.setValue('D:/blog')
    await flushPromises()
    expect(document.body.textContent).toContain('Drop your memory edits?')
    expect(memory).not.toHaveBeenCalledWith('D:/blog')
    // Keep editing: the picker shows the project the edits belong to.
    dialogButton('Cancel').click()
    await flushPromises()
    expect((select.element as HTMLSelectElement).value).toBe('D:/shop')
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value).toBe('my edit')
    // Ask again and drop them.
    await select.setValue('D:/blog')
    await flushPromises()
    dialogButton('Drop and switch').click()
    await flushPromises()
    expect(memory).toHaveBeenCalledWith('D:/blog')
    expect((wrapper.get('textarea').element as HTMLTextAreaElement).value).toBe('notes of D:/blog')
    wrapper.unmount()
  })

  it('switches at once when nothing is edited', async () => {
    const { wrapper, memory } = await mountSection()
    await wrapper.get('select').setValue('D:/blog')
    await flushPromises()
    expect(document.body.textContent).not.toContain('Drop your memory edits?')
    expect(memory).toHaveBeenCalledWith('D:/blog')
    wrapper.unmount()
  })
})
