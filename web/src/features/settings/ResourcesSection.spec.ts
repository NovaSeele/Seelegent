import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/api/client'
import type { SettingsApi } from '@/api/settings'
import type { Health, Settings } from '@/api/types'
import { useConnectionStore } from '@/stores/connection'
import { useSettingsStore } from '@/stores/settings'
import { setupApp } from '@/test/app'

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
    ...extra,
  }
}

const WINDOWS_ONLY = ['minFreeCommit', 'workerMemoryLimit', 'workerCpuRate']

async function mountSection(
  patch: SettingsApi['patch'] = vi.fn<SettingsApi['patch']>(),
  h: Health = health(),
) {
  const { plugins } = await setupApp()
  const connection = useConnectionStore()
  connection.settings = SETTINGS
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
    expect(memory.text()).toContain('0 to 1048576 (0 = off)')
    expect(wrapper.get('[data-setting="gateConcurrency"]').text()).toContain('0 = no limit')
    expect(wrapper.get('[data-setting="workerCpuRate"]').text()).toContain('whole machine')
    wrapper.unmount()
  })

  it('marks the Windows-only settings, without dimming them on Windows', async () => {
    const wrapper = await mountSection()
    for (const item of WINDOWS_ONLY) {
      const row = wrapper.get(`[data-setting="${item}"]`)
      expect(row.get('[data-os-note]').text(), item).toBe('Windows only.')
      expect(row.classes()).not.toContain('sf-row--inert')
    }
    expect(wrapper.find('[data-setting="minFreeMemory"] [data-os-note]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('says a Windows-only setting has no effect on another system, and dims it', async () => {
    const linux = health({
      os: 'linux',
      memory: { availPhysMB: 900, availCommitMB: 0, known: true },
    })
    const wrapper = await mountSection(undefined, linux)
    for (const item of WINDOWS_ONLY) {
      const row = wrapper.get(`[data-setting="${item}"]`)
      expect(row.get('[data-os-note]').text(), item).toContain('has no effect')
      expect(row.classes()).toContain('sf-row--inert')
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
