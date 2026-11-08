import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { h, ref } from 'vue'

import { ApiError } from '@/api/client'
import type { SettingsApi } from '@/api/settings'
import type { Health, Settings } from '@/api/types'
import Dialog from '@/components/ui/Dialog.vue'
import { useConnectionStore } from '@/stores/connection'
import { useLayersStore } from '@/stores/layers'
import { useSettingsStore } from '@/stores/settings'
import { flush, installKeys, press, setupApp } from '@/test/app'
import { makeHealthResources } from '@/test/fixtures'

import { FIELDS } from './fields'
import ResourcesSection from './ResourcesSection.vue'
import { searchSettings } from './search'
import { sectionInfo } from './sections'

const SETTINGS = {
  port: 47831,
  workerPriority: 'low',
  gatePriority: 'low',
  maxConcurrentWorkers: 0,
  gateConcurrency: 2,
  minFreeMemoryMB: 2048,
  minFreeCommitMB: 4096,
  criticalFreeMemoryMB: 1024,
  workerMemoryLimitMB: 0,
  workerCPURatePercent: 0,
  testParallelism: 4,
  daemonMemoryLimitMB: 0,
} as unknown as Settings

function health(extra: Partial<Health> = {}): Health {
  return {
    ok: true,
    version: '1',
    commit: 'c',
    build: 'b',
    apiVersion: 1,
    pid: 1,
    port: 47831,
    startedAt: '2026-11-08T00:00:00Z',
    homeId: '0123456789abcdef',
    os: 'windows',
    memory: { availPhysMB: 12595, availCommitMB: 20480, known: true },
    lowMemory: false,
    ...makeHealthResources(),
    ...extra,
  }
}

const WINDOWS_ONLY = ['minFreeCommit', 'workerMemoryLimit', 'workerCpuRate']

/** The five machine-sized settings; AUTO_SETTINGS has none of them: all auto. */
const AUTO_KEYS = [
  'gateConcurrency',
  'minFreeMemoryMB',
  'minFreeCommitMB',
  'criticalFreeMemoryMB',
  'testParallelism',
] as const
const AUTO_SETTINGS = Object.fromEntries(
  Object.entries(SETTINGS).filter(([key]) => !(AUTO_KEYS as readonly string[]).includes(key)),
) as unknown as Settings

/** A patch that answers like the daemon: the settings with the body merged (null = auto). */
const echo = () =>
  vi.fn<SettingsApi['patch']>((body) =>
    Promise.resolve({ ...useConnectionStore().settings, ...body } as Settings),
  )

async function mountSection(
  patch: SettingsApi['patch'] = vi.fn<SettingsApi['patch']>(),
  h: Health = health(),
  settings: Settings = SETTINGS,
) {
  const { plugins } = await setupApp()
  const connection = useConnectionStore()
  connection.settings = settings
  connection.health = h
  // The section reads /health again when it opens; here it keeps the reading set above.
  vi.spyOn(connection, 'refreshHealth').mockResolvedValue()
  useSettingsStore().setApi({ patch } as unknown as SettingsApi)
  return mount(ResourcesSection, { global: { plugins }, attachTo: document.body })
}

describe('Resources settings', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    vi.useRealTimers()
  })

  it('shows all 11 resource settings, the priorities as selects, the MB fields with their unit', async () => {
    const wrapper = await mountSection()
    expect(FIELDS.resources).toHaveLength(11)
    for (const def of FIELDS.resources) {
      expect(wrapper.find(`[data-setting="${def.item}"]`).exists(), def.item).toBe(true)
    }
    expect(wrapper.find('[data-setting="workerPriority"] input').exists()).toBe(false)
    expect(wrapper.get('[data-setting="workerPriority"]').text()).toContain('Low')
    const memory = wrapper.get('[data-setting="minFreeMemory"]')
    expect(memory.text()).toContain('MB')
    // Ranges and the saved amount read in GB or TB, not as a long number of MB.
    expect(memory.text()).toContain('0 to 1 TB (0 = off)')
    expect(memory.text()).toContain('= 2 GB')
    expect(wrapper.get('[data-setting="workerMemoryLimit"]').text()).not.toContain('= 0')
    expect(wrapper.get('[data-setting="gateConcurrency"]').text()).toContain('0 = no limit')
    expect(wrapper.get('[data-setting="workerCpuRate"]').text()).toContain('whole machine')
    wrapper.unmount()
  })

  it('marks the Windows-only settings, without dimming them on Windows', async () => {
    const wrapper = await mountSection()
    for (const item of WINDOWS_ONLY) {
      const row = wrapper.get(`[data-setting="${item}"]`)
      expect(row.get('[data-os-note]').text(), item).toBe('Windows only.')
      // A screen reader hears the note with the field.
      const notes = row.get('.sf__notes').attributes('id')
      expect(row.get('input').attributes('aria-describedby'), item).toContain(notes)
    }
    const memory = wrapper.get('[data-setting="minFreeMemory"]')
    expect(memory.get('input').attributes('aria-describedby')).toContain(
      memory.get('[data-now]').element.parentElement?.id,
    )
    expect(wrapper.find('[data-setting="minFreeMemory"] [data-os-note]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('says a Windows-only setting has no effect on another system, and keeps it editable', async () => {
    const linux = health({
      os: 'linux',
      memory: { availPhysMB: 900, availCommitMB: 0, known: true },
    })
    const wrapper = await mountSection(undefined, linux)
    for (const item of WINDOWS_ONLY) {
      const row = wrapper.get(`[data-setting="${item}"]`)
      expect(row.get('[data-os-note]').text(), item).toContain('has no effect')
      // Settings sync across machines: the field stays editable and is not dimmed.
      expect(row.get('input').attributes('disabled'), item).toBeUndefined()
      expect(row.html(), item).not.toContain('opacity')
    }
    // Commit is unknown off Windows: no "now" line for it, physical memory in MB below 1 GB.
    expect(wrapper.find('[data-setting="minFreeCommit"] [data-now]').exists()).toBe(false)
    expect(wrapper.get('[data-setting="minFreeMemory"] [data-now]').text()).toBe('Now: 900 MB free')
    wrapper.unmount()
  })

  it('shows the free memory and commit now next to their thresholds, and nothing when unknown', async () => {
    const wrapper = await mountSection()
    expect(wrapper.get('[data-setting="minFreeMemory"] [data-now]').text()).toBe(
      'Now: 12.3 GB free',
    )
    expect(wrapper.get('[data-setting="minFreeCommit"] [data-now]').text()).toBe('Now: 20 GB free')
    expect(wrapper.get('[data-setting="criticalFreeMemory"] [data-now]').text()).toBe(
      'Now: 12.3 GB free',
    )
    useConnectionStore().health = health({
      memory: { availPhysMB: 0, availCommitMB: 0, known: false },
    })
    await flushPromises()
    expect(wrapper.find('[data-now]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('reads /health when it opens and every 10 s while open, and stops once closed', async () => {
    vi.useFakeTimers()
    const wrapper = await mountSection()
    const refresh = vi.mocked(useConnectionStore().refreshHealth)
    expect(refresh).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(10_000)
    expect(refresh).toHaveBeenCalledTimes(2)
    wrapper.unmount()
    await vi.advanceTimersByTimeAsync(30_000)
    expect(refresh).toHaveBeenCalledTimes(2)
  })

  it('refuses an emergency floor at or above the start threshold on both fields, without sending', async () => {
    const patch = vi.fn<SettingsApi['patch']>()
    const wrapper = await mountSection(patch)
    const critical = () => wrapper.get('[data-setting="criticalFreeMemory"]')
    const min = () => wrapper.get('[data-setting="minFreeMemory"]')
    const input = critical().get('input')
    await input.setValue('2048')
    await input.trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(patch).not.toHaveBeenCalled()
    const message = 'The emergency memory floor must be below Free memory to start'
    expect(critical().text()).toContain(message)
    expect(min().text()).toContain(message)
    expect(min().get('input').attributes('aria-invalid')).toBe('true')
    // Esc back to the saved value clears it from both.
    await input.setValue('3000')
    await input.trigger('keydown', { key: 'Escape' })
    await flushPromises()
    expect(critical().text()).not.toContain(message)
    expect(min().text()).not.toContain(message)
    wrapper.unmount()
  })

  it('shows the daemon’s cross-field error on both fields, and clears both once one saves', async () => {
    const patch = vi
      .fn<SettingsApi['patch']>()
      .mockRejectedValueOnce(
        new ApiError(400, 'invalid_argument', '1 setting(s) are not valid', {
          fields: [
            {
              field: 'criticalFreeMemoryMB',
              message: 'must be below minFreeMemoryMB (4000) while both are on (got 5000)',
            },
          ],
        }),
      )
      .mockImplementation((body) => Promise.resolve({ ...SETTINGS, ...body } as Settings))
    const wrapper = await mountSection(patch)
    // The dashboard's own check passes (it knows an older minFreeMemoryMB): the daemon refuses.
    useConnectionStore().settings = { ...SETTINGS, minFreeMemoryMB: 8000 }
    const input = wrapper.get('[data-setting="criticalFreeMemory"] input')
    await input.setValue('5000')
    await input.trigger('keydown', { key: 'Enter' })
    await flushPromises()
    const message = 'must be below minFreeMemoryMB (4000)'
    expect(wrapper.get('[data-setting="criticalFreeMemory"]').text()).toContain(message)
    expect(wrapper.get('[data-setting="minFreeMemory"]').text()).toContain(message)
    await input.setValue('1000')
    await input.trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(patch).toHaveBeenLastCalledWith({ criticalFreeMemoryMB: 1000 })
    expect(wrapper.text()).not.toContain(message)
    wrapper.unmount()
  })

  it('sends a refused floor again once the other field fixes the clash (min 2048, critical 1024)', async () => {
    const patch = vi.fn<SettingsApi['patch']>((body) => {
      const next = { ...useConnectionStore().settings, ...body } as Settings
      return Promise.resolve(next)
    })
    const wrapper = await mountSection(patch)
    const critical = () => wrapper.get('[data-setting="criticalFreeMemory"]')
    const min = () => wrapper.get('[data-setting="minFreeMemory"]')
    const message = 'The emergency memory floor must be below Free memory to start'
    await critical().get('input').setValue('3000')
    await critical().get('input').trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(patch).not.toHaveBeenCalled()
    expect(critical().text()).toContain(message)
    expect(min().text()).toContain(message)

    await min().get('input').setValue('4096')
    await min().get('input').trigger('keydown', { key: 'Enter' })
    await flushPromises()
    // The floor typed earlier is valid now: it is sent, not left in the field unsaved and without an error.
    expect(patch.mock.calls.map(([body]) => body)).toEqual([
      { minFreeMemoryMB: 4096 },
      { criticalFreeMemoryMB: 3000 },
    ])
    expect(useConnectionStore().settings?.criticalFreeMemoryMB).toBe(3000)
    expect(critical().get('input').element.value).toBe('3000')
    expect(wrapper.text()).not.toContain(message)
    wrapper.unmount()
  })

  it('shows the clash again when the other field saves but the refused floor is still too high', async () => {
    const patch = vi.fn<SettingsApi['patch']>((body) =>
      Promise.resolve({ ...useConnectionStore().settings, ...body } as Settings),
    )
    const wrapper = await mountSection(patch)
    const critical = () => wrapper.get('[data-setting="criticalFreeMemory"]')
    const message = 'The emergency memory floor must be below Free memory to start'
    await critical().get('input').setValue('3000')
    await critical().get('input').trigger('keydown', { key: 'Enter' })
    await flushPromises()
    const min = wrapper.get('[data-setting="minFreeMemory"] input')
    await min.setValue('2500')
    await min.trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(patch.mock.calls.map(([body]) => body)).toEqual([{ minFreeMemoryMB: 2500 }])
    expect(critical().text()).toContain(message)
    expect(critical().get('input').attributes('aria-invalid')).toBe('true')
    expect(critical().get('input').element.value).toBe('3000')
    wrapper.unmount()
  })

  it('sends a floor the daemon refused again once the other field saves', async () => {
    const patch = vi
      .fn<SettingsApi['patch']>()
      .mockRejectedValueOnce(
        new ApiError(400, 'invalid_argument', '1 setting(s) are not valid', {
          fields: [
            {
              field: 'criticalFreeMemoryMB',
              message: 'must be below minFreeMemoryMB (2048) while both are on (got 3000)',
            },
          ],
        }),
      )
      .mockImplementation((body) =>
        Promise.resolve({ ...useConnectionStore().settings, ...body } as Settings),
      )
    const wrapper = await mountSection(patch)
    // The dashboard knows an older minFreeMemoryMB, so its own check lets 3000 through and the daemon refuses.
    useConnectionStore().settings = { ...SETTINGS, minFreeMemoryMB: 8000 }
    const critical = wrapper.get('[data-setting="criticalFreeMemory"] input')
    await critical.setValue('3000')
    await critical.trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(wrapper.text()).toContain('must be below minFreeMemoryMB (2048)')
    const min = wrapper.get('[data-setting="minFreeMemory"] input')
    await min.setValue('4096')
    await min.trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(patch.mock.calls.map(([body]) => body)).toEqual([
      { criticalFreeMemoryMB: 3000 },
      { minFreeMemoryMB: 4096 },
      { criticalFreeMemoryMB: 3000 },
    ])
    expect(wrapper.text()).not.toContain('must be below')
    wrapper.unmount()
  })

  it('keeps a plain range error on its own field', async () => {
    const patch = vi.fn<SettingsApi['patch']>(() =>
      Promise.reject(
        new ApiError(400, 'invalid_argument', 'invalid', {
          fields: [{ field: 'criticalFreeMemoryMB', message: 'not while tasks run' }],
        }),
      ),
    )
    const wrapper = await mountSection(patch)
    const input = wrapper.get('[data-setting="criticalFreeMemory"] input')
    await input.setValue('512')
    await input.trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(wrapper.get('[data-setting="criticalFreeMemory"]').text()).toContain(
      'not while tasks run',
    )
    expect(wrapper.get('[data-setting="minFreeMemory"]').text()).not.toContain(
      'not while tasks run',
    )
    wrapper.unmount()
  })

  it('shows an auto setting as Auto with the value in use on this machine, live from Health', async () => {
    const wrapper = await mountSection(undefined, health(), AUTO_SETTINGS)
    const row = (item: string) => wrapper.get(`[data-setting="${item}"]`)
    for (const item of [
      'minFreeMemory',
      'criticalFreeMemory',
      'minFreeCommit',
      'testParallelism',
      'gateConcurrency',
    ]) {
      expect(row(item).find('input').exists(), item).toBe(false)
      expect(row(item).get('[data-auto]').text(), item).toMatch(/^Auto/)
      expect(row(item).find('[data-explicit]').exists(), item).toBe(false)
    }
    expect(row('minFreeMemory').get('[data-auto]').text()).toBe('Auto: 2 GB on this machine')
    expect(row('minFreeCommit').get('[data-auto]').text()).toBe('Auto: 2.4 GB on this machine')
    expect(row('testParallelism').get('[data-auto]').text()).toBe('Auto: 2 on this machine')
    // The free memory now stays next to the threshold, and the Set button is described by both.
    const set = row('minFreeMemory').get('[data-auto-set]')
    expect(set.attributes('aria-label')).toBe('Set Free memory to start yourself')
    const described = set.attributes('aria-describedby')?.split(' ') ?? []
    expect(described).toContain(row('minFreeMemory').get('[data-auto]').attributes('id'))
    expect(described).toContain(row('minFreeMemory').get('.sf__notes').attributes('id'))
    // Settings that are not machine-sized keep their plain field and Default button.
    expect(row('maxWorkers').find('input').exists()).toBe(true)
    expect(row('maxWorkers').text()).toContain('Default')

    const next = health()
    next.resources = {
      ...next.resources,
      minFreeMemoryMB: { value: 1024, source: 'auto' },
      minFreeCommitMB: { value: 0, source: 'auto' },
    }
    useConnectionStore().health = next
    await flushPromises()
    expect(row('minFreeMemory').get('[data-auto]').text()).toBe('Auto: 1 GB on this machine')
    // An auto value of 0 reads as the field's 0: off.
    expect(row('minFreeCommit').get('[data-auto]').text()).toBe('Auto: off on this machine')
    wrapper.unmount()
  })

  it('shows the machine the auto values come from', async () => {
    const wrapper = await mountSection()
    expect(wrapper.get('[data-machine]').text()).toBe(
      'This machine: 16 GB RAM, 24 GB commit limit, 8 CPUs',
    )
    useConnectionStore().health = health({
      os: 'linux',
      machine: { totalPhysMB: 8192, totalCommitMB: 0, cpus: 1 },
    })
    await flushPromises()
    expect(wrapper.get('[data-machine]').text()).toBe('This machine: 8 GB RAM, 1 CPU')
    useConnectionStore().health = health({
      machine: { totalPhysMB: 0, totalCommitMB: 0, cpus: 4 },
    })
    await flushPromises()
    expect(wrapper.get('[data-machine]').text()).toBe('This machine: 4 CPUs')
    wrapper.unmount()
  })

  it('sets an auto setting from the value in use, then puts it back to auto with null', async () => {
    const patch = echo()
    const wrapper = await mountSection(patch, health(), AUTO_SETTINGS)
    const row = () => wrapper.get('[data-setting="minFreeMemory"]')
    await row().get('[data-auto-set]').trigger('click')
    await flushPromises()
    const input = row().get('input')
    expect(input.element.value).toBe('2048')
    expect(document.activeElement).toBe(input.element)
    await input.setValue('3072')
    await input.trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(patch.mock.calls.map(([body]) => body)).toEqual([{ minFreeMemoryMB: 3072 }])
    expect(row().find('[data-auto]').exists()).toBe(false)
    expect(row().get('[data-explicit]').text()).toBe('Set by you.')
    // A screen reader hears that the user set it, with the field.
    expect(row().get('input').attributes('aria-describedby')).toContain(
      row().get('.sf__notes').attributes('id'),
    )
    const useAuto = row().get('[data-auto-reset]')
    expect(useAuto.text()).toBe('Use auto')
    expect(useAuto.attributes('aria-label')).toBe(
      'Set Free memory to start back to auto, sized to this machine',
    )
    ;(useAuto.element as HTMLElement).focus()
    await useAuto.trigger('click')
    await flushPromises()
    expect(patch.mock.calls.map(([body]) => body)).toEqual([
      { minFreeMemoryMB: 3072 },
      { minFreeMemoryMB: null },
    ])
    expect(row().get('[data-auto]').text()).toMatch(/^Auto/)
    expect(row().find('[data-explicit]').exists()).toBe(false)
    // The focus stays in the row: on Set, which took the place of Use auto.
    expect(document.activeElement).toBe(row().get('[data-auto-set]').element)
    wrapper.unmount()
  })

  it('closes a field opened from Auto on Esc, without closing the settings dialog', async () => {
    const { plugins } = await setupApp()
    const uninstall = installKeys()
    const connection = useConnectionStore()
    connection.settings = AUTO_SETTINGS
    connection.health = health()
    vi.spyOn(connection, 'refreshHealth').mockResolvedValue()
    const patch = echo()
    useSettingsStore().setApi({ patch } as unknown as SettingsApi)
    const open = ref(true)
    const wrapper = mount(
      {
        setup: () => () =>
          h(
            Dialog,
            {
              open: open.value,
              title: 'Settings',
              'onUpdate:open': (v: boolean) => (open.value = v),
            },
            { default: () => h(ResourcesSection) },
          ),
      },
      { attachTo: document.body, global: { plugins } },
    )
    await flush()
    const row = () => document.querySelector('[data-setting="gateConcurrency"]') as HTMLElement
    ;(row().querySelector('[data-auto-set]') as HTMLButtonElement).click()
    await flush()
    const input = row().querySelector('input') as HTMLInputElement
    expect(document.activeElement).toBe(input)
    // The field starts at the value in use: nothing to undo, and Esc still must not reach the dialog.
    press('Escape')
    await flush()
    expect(open.value).toBe(true)
    expect(useLayersStore().stack).toHaveLength(1)
    expect(row().querySelector('input')).toBeNull()
    expect(row().querySelector('[data-auto]')?.textContent?.trim()).toBe('Auto: 1 on this machine')
    expect(document.activeElement).toBe(row().querySelector('[data-auto-set]'))
    // With a typed value, Esc puts the value in use back and closes the field the same way.
    ;(row().querySelector('[data-auto-set]') as HTMLButtonElement).click()
    await flush()
    const again = row().querySelector('input') as HTMLInputElement
    again.value = '3'
    again.dispatchEvent(new Event('input'))
    press('Escape')
    await flush()
    expect(open.value).toBe(true)
    expect(row().querySelector('input')).toBeNull()
    expect(document.activeElement).toBe(row().querySelector('[data-auto-set]'))
    expect(patch).not.toHaveBeenCalled()
    // Once back on Set, Esc closes the dialog as usual.
    press('Escape')
    await flush()
    expect(open.value).toBe(false)
    wrapper.unmount()
    uninstall()
  })

  it('opens the field when the Auto box is clicked, without a second tab stop', async () => {
    const wrapper = await mountSection(echo(), health(), AUTO_SETTINGS)
    const row = () => wrapper.get('[data-setting="minFreeMemory"]')
    expect(row().get('[data-auto]').attributes('tabindex')).toBeUndefined()
    await row().get('[data-auto]').trigger('click')
    await flushPromises()
    expect(document.activeElement).toBe(row().get('input').element)
    expect(row().get('input').element.value).toBe('2048')
    wrapper.unmount()
  })

  it('goes back to Auto without sending when the value in use is left as it is', async () => {
    const patch = echo()
    const wrapper = await mountSection(patch, health(), AUTO_SETTINGS)
    const row = () => wrapper.get('[data-setting="testParallelism"]')
    await row().get('[data-auto-set]').trigger('click')
    await flushPromises()
    await row().get('input').trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(patch).not.toHaveBeenCalled()
    expect(row().find('input').exists()).toBe(false)
    expect(document.activeElement).toBe(row().get('[data-auto-set]').element)
    // Tab out of it (to "Use auto", which "Set" replaces): the focus lands on "Set", not on the page.
    await row().get('[data-auto-set]').trigger('click')
    await flushPromises()
    ;(row().get('[data-auto-reset]').element as HTMLElement).focus()
    await flushPromises()
    expect(row().find('input').exists()).toBe(false)
    expect(document.activeElement).toBe(row().get('[data-auto-set]').element)
    // "Use auto" on a field opened from Auto closes it, without a request either.
    await row().get('[data-auto-set]').trigger('click')
    await flushPromises()
    await row().get('[data-auto-reset]').trigger('click')
    await flushPromises()
    expect(patch).not.toHaveBeenCalled()
    expect(row().get('[data-auto]').text()).toBe('Auto: 2 on this machine')
    wrapper.unmount()
  })

  it('takes 0 on an auto setting and says what it means', async () => {
    const patch = echo()
    const wrapper = await mountSection(patch, health(), AUTO_SETTINGS)
    const row = () => wrapper.get('[data-setting="gateConcurrency"]')
    await row().get('[data-auto-set]').trigger('click')
    await flushPromises()
    await row().get('input').setValue('0')
    await row().get('input').trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(patch).toHaveBeenLastCalledWith({ gateConcurrency: 0 })
    expect(row().get('input').element.value).toBe('0')
    expect(row().get('[data-explicit]').text()).toBe('Set by you: no limit.')
    wrapper.unmount()
  })

  it('patches only the field that changed, never the values in use of the auto settings', async () => {
    const patch = echo()
    const wrapper = await mountSection(patch, health(), AUTO_SETTINGS)
    for (const [item, value] of [
      ['maxWorkers', '3'],
      ['workerMemoryLimit', '4096'],
      ['daemonMemoryLimit', '512'],
    ] as const) {
      const input = wrapper.get(`[data-setting="${item}"] input`)
      await input.setValue(value)
      await input.trigger('keydown', { key: 'Enter' })
      await flushPromises()
    }
    expect(patch.mock.calls.map(([body]) => body)).toEqual([
      { maxConcurrentWorkers: 3 },
      { workerMemoryLimitMB: 4096 },
      { daemonMemoryLimitMB: 512 },
    ])
    for (const [body] of patch.mock.calls) {
      for (const key of AUTO_KEYS) expect(Object.keys(body), key).not.toContain(key)
    }
    // The auto settings are still auto (absent) after those saves.
    for (const key of AUTO_KEYS) expect(useConnectionStore().settings?.[key], key).toBeUndefined()
    // Setting one auto setting patches that one alone.
    const critical = () => wrapper.get('[data-setting="criticalFreeMemory"]')
    await critical().get('[data-auto-set]').trigger('click')
    await flushPromises()
    await critical().get('input').setValue('512')
    await critical().get('input').trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(patch).toHaveBeenLastCalledWith({ criticalFreeMemoryMB: 512 })
    wrapper.unmount()
  })

  it('names only a field the user set in a cross-field error', async () => {
    const patch = vi.fn<SettingsApi['patch']>().mockRejectedValueOnce(
      new ApiError(400, 'invalid_argument', '1 setting(s) are not valid', {
        fields: [
          {
            field: 'criticalFreeMemoryMB',
            message: 'must be below minFreeMemoryMB (2048) while both are on (got 3000)',
          },
        ],
      }),
    )
    const wrapper = await mountSection(patch, health(), AUTO_SETTINGS)
    // The start threshold is auto: the dashboard's own check does not apply, and the daemon's error stays on the
    // floor the user typed, not on the auto setting it names.
    const critical = () => wrapper.get('[data-setting="criticalFreeMemory"]')
    await critical().get('[data-auto-set]').trigger('click')
    await flushPromises()
    await critical().get('input').setValue('3000')
    await critical().get('input').trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(patch).toHaveBeenCalledWith({ criticalFreeMemoryMB: 3000 })
    expect(critical().text()).toContain('must be below minFreeMemoryMB')
    const min = wrapper.get('[data-setting="minFreeMemory"]')
    expect(min.text()).not.toContain('must be below')
    expect(min.find('[aria-invalid]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('reads /health again after a save, so the values in use follow the settings', async () => {
    const wrapper = await mountSection(echo(), health(), AUTO_SETTINGS)
    const refresh = vi.mocked(useConnectionStore().refreshHealth)
    expect(refresh).toHaveBeenCalledTimes(1)
    const input = wrapper.get('[data-setting="maxWorkers"] input')
    await input.setValue('2')
    await input.trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(refresh).toHaveBeenCalledTimes(2)
    // Until it answers, Health may still have the value the user had set: the row says Auto, no stale number.
    useConnectionStore().health = health({
      resources: {
        ...makeHealthResources().resources,
        minFreeMemoryMB: { value: 4096, source: 'explicit' },
      },
    })
    await flushPromises()
    expect(wrapper.get('[data-setting="minFreeMemory"] [data-auto]').text()).toBe('Auto')
    wrapper.unmount()
  })

  it('says when an auto threshold is worked out from the other one the user set', async () => {
    const settings = { ...AUTO_SETTINGS, minFreeMemoryMB: 3072 } as Settings
    // 'derived' is the daemon's coming third source; types.gen.ts does not have it yet.
    const derived = (value: number) => ({ value, source: 'derived' }) as never
    const wrapper = await mountSection(
      echo(),
      health({
        resources: {
          ...makeHealthResources().resources,
          minFreeMemoryMB: { value: 3072, source: 'explicit' },
          criticalFreeMemoryMB: derived(1536),
        },
      }),
      settings,
    )
    expect(wrapper.get('[data-setting="criticalFreeMemory"] [data-auto]').text()).toBe(
      'Auto: half of your Free memory to start, 1.5 GB',
    )
    // "Set" starts from the derived value.
    await wrapper.get('[data-setting="criticalFreeMemory"] [data-auto-set]').trigger('click')
    await flushPromises()
    const input = wrapper.get('[data-setting="criticalFreeMemory"] input')
      .element as HTMLInputElement
    expect(input.value).toBe('1536')
    wrapper.unmount()

    const twice = await mountSection(
      echo(),
      health({
        resources: {
          ...makeHealthResources().resources,
          minFreeMemoryMB: derived(6144),
          criticalFreeMemoryMB: { value: 3072, source: 'explicit' },
        },
      }),
      { ...AUTO_SETTINGS, criticalFreeMemoryMB: 3072 } as Settings,
    )
    expect(twice.get('[data-setting="minFreeMemory"] [data-auto]').text()).toBe(
      'Auto: twice your Emergency memory floor, 6 GB',
    )
    twice.unmount()
  })

  it('is found by search, in English and in another language', async () => {
    await setupApp('en')
    expect(sectionInfo('resources').items).toHaveLength(11)
    expect(searchSettings('priority').map((h) => h.item)).toEqual(
      expect.arrayContaining(['workerPriority', 'gatePriority']),
    )
    expect(searchSettings('free memory')[0]).toMatchObject({
      section: 'resources',
      item: 'minFreeMemory',
    })
    expect(searchSettings('GOMEMLIMIT')[0]).toMatchObject({ item: 'daemonMemoryLimit' })
    expect(searchSettings('CPU').map((h) => h.item)).toContain('workerCpuRate')
    await setupApp('vi')
    expect(searchSettings('tài nguyên').map((h) => h.section)).toContain('resources')
  })
})
