import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { createApiClient } from '@/api/client'
import type { BootState, Health } from '@/api/types'
import { useConnectionStore } from '@/stores/connection'
import { useTasksStore } from '@/stores/tasks'
import { FakeEventSource, setupApp } from '@/test/app'
import { iso, makeHealthResources, makeTask } from '@/test/fixtures'

import MemoryBanner from './MemoryBanner.vue'

function health(extra: Partial<Health> = {}): Health {
  return {
    ok: true,
    version: '1',
    commit: 'c',
    build: 'b1',
    apiVersion: 1,
    pid: 1,
    port: 47831,
    startedAt: iso(),
    homeId: '0123456789abcdef',
    os: 'windows',
    memory: { availPhysMB: 700, availCommitMB: 2150, known: true },
    lowMemory: false,
    ...makeHealthResources(),
    ...extra,
  }
}

/** Health.resources with the emergency floor in use at `mb` (auto unless said). */
function floorAt(mb: number, source: 'auto' | 'explicit' = 'auto'): Pick<Health, 'resources'> {
  const { resources } = makeHealthResources()
  return { resources: { ...resources, criticalFreeMemoryMB: { value: mb, source } } }
}

const json = (body: unknown) =>
  new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })

describe('MemoryBanner', () => {
  beforeEach(() => {
    FakeEventSource.instances = []
  })
  afterEach(() => {
    useConnectionStore().stop()
  })

  async function mounted() {
    const { plugins } = await setupApp()
    const wrapper = mount(MemoryBanner, {
      global: { plugins, stubs: { RouterLink: RouterLinkStub } },
    })
    return { wrapper, connection: useConnectionStore() }
  }

  it('shows while the daemon holds new starts, with the free amounts, and goes when it stops', async () => {
    const { wrapper, connection } = await mounted()
    connection.status = 'online'
    connection.health = health({ lowMemory: true })
    await flushPromises()
    const banner = wrapper.get('[role="status"]')
    expect(banner.text()).toContain('Machine low on memory: new starts held')
    expect(banner.text()).toContain('700 MB memory free')
    expect(banner.text()).toContain('2.1 GB commit free')
    expect(wrapper.getComponent(RouterLinkStub).props('to')).toEqual({
      name: 'settings',
      params: { section: 'resources' },
    })
    connection.health = health({ lowMemory: false })
    await flushPromises()
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
  })

  it('says which floor the machine is under and how many tasks wait for memory', async () => {
    const { wrapper, connection } = await mounted()
    connection.status = 'online'
    connection.settings = { criticalFreeMemoryMB: 1024 } as never
    connection.health = health({ lowMemory: true, ...floorAt(1024, 'explicit') })
    await flushPromises()
    const banner = wrapper.get('[role="status"]')
    expect(banner.text()).toContain('below the Emergency memory floor of 1 GB')
    expect(banner.find('[data-waiting]').exists()).toBe(false)

    const tasks = useTasksStore()
    tasks.upsert(makeTask({ id: 'a', status: 'waiting_quota', waiting: 'memory' }))
    tasks.upsert(makeTask({ id: 'b', status: 'waiting_quota', waiting: 'memory' }))
    tasks.upsert(makeTask({ id: 'c', status: 'waiting_quota' }))
    tasks.upsert(makeTask({ id: 'd', status: 'running' }))
    await flushPromises()
    expect(wrapper.get('[data-waiting]').text()).toBe('2 tasks waiting')

    connection.settings = { criticalFreeMemoryMB: 0 } as never
    connection.health = health({ lowMemory: true, ...floorAt(0, 'explicit') })
    await flushPromises()
    expect(wrapper.get('[role="status"]').text()).not.toContain('floor')
  })

  it('names the floor in use while the setting is auto (absent from the settings)', async () => {
    const { wrapper, connection } = await mounted()
    connection.status = 'online'
    connection.settings = {} as never
    connection.health = health({ lowMemory: true, ...floorAt(1536) })
    await flushPromises()
    expect(wrapper.get('[role="status"]').text()).toContain(
      'below the Emergency memory floor of 1.5 GB',
    )
  })

  it('gives free memory just under the floor in MB, so it does not read the same as the floor', async () => {
    const { wrapper, connection } = await mounted()
    connection.status = 'online'
    connection.health = health({
      lowMemory: true,
      ...floorAt(2048),
      memory: { availPhysMB: 2040, availCommitMB: 9000, known: true },
    })
    await flushPromises()
    const text = wrapper.get('[role="status"]').text()
    expect(text).toContain('2,040 MB memory free')
    expect(text).toContain('of 2,048 MB')
  })

  it('leaves out an unknown reading and hides while offline', async () => {
    const { wrapper, connection } = await mounted()
    connection.status = 'online'
    connection.health = health({
      lowMemory: true,
      memory: { availPhysMB: 0, availCommitMB: 0, known: false },
    })
    await flushPromises()
    expect(wrapper.get('[role="status"]').text()).not.toContain('free')
    connection.status = 'offline'
    await flushPromises()
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
  })

  it('follows the boot state and the resources.changed frame', async () => {
    const { wrapper, connection } = await mounted()
    const boot = {
      health: health({ lowMemory: true }),
      settings: {},
      accounts: [],
      backends: [],
      tasks: [],
      projects: [],
      lastEventId: 'a1.1',
    } as unknown as BootState
    connection.start({
      api: createApiClient({ fetch: vi.fn<typeof fetch>(async () => json(boot)) }),
      createSource: (url) => new FakeEventSource(url),
      reload: () => {},
      random: () => 0,
    })
    await flushPromises()
    const source = FakeEventSource.last
    source.open()
    await flushPromises()
    expect(wrapper.find('[role="status"]').exists()).toBe(true)

    const memory = { availPhysMB: 4096, availCommitMB: 9000, known: true }
    source.emit('resources.changed', JSON.stringify({ lowMemory: false, memory }), 'a1.2')
    await flushPromises()
    expect(connection.health?.memory).toEqual(memory)
    expect(wrapper.find('[role="status"]').exists()).toBe(false)

    source.emit(
      'resources.changed',
      JSON.stringify({ lowMemory: true, memory: { ...memory, availPhysMB: 800 } }),
      'a1.3',
    )
    await flushPromises()
    expect(wrapper.get('[role="status"]').text()).toContain('800 MB memory free')
  })
})
