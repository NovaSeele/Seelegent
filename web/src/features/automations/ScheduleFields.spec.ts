/** The schedule fields: errors under the cron field, and next runs that always belong to the latest edit. */
import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, ref } from 'vue'

import { createApiClient, createAutomationsApi } from '@/api'
import { useAutomationsStore } from '@/stores/automations'
import { setupApp } from '@/test/app'

import { toForm, type AutomationForm, type FormProblem } from './automationModel'
import ScheduleFields from './ScheduleFields.vue'

const json = (body: unknown) =>
  new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })

async function mountFields(fetch: typeof globalThis.fetch, cron: string) {
  const { plugins } = await setupApp()
  useAutomationsStore().setApi(createAutomationsApi(createApiClient({ fetch })))
  const form = ref<AutomationForm>({ ...toForm(null), mode: 'cron', cron, timezone: 'UTC' })
  const problems = ref<FormProblem[]>([])
  const Host = defineComponent(
    () => () =>
      h(ScheduleFields, {
        modelValue: form.value,
        'onUpdate:modelValue': (v: AutomationForm) => (form.value = v),
        problems: problems.value,
      }),
  )
  const wrapper = mount(Host, { global: { plugins } })
  return { wrapper, form, problems }
}

describe('ScheduleFields', () => {
  afterEach(() => vi.useRealTimers())

  it('drops the answer to an older edit', async () => {
    vi.useFakeTimers()
    let release: (r: Response) => void = () => {}
    const fetch = vi.fn<typeof globalThis.fetch>(async (_input, init) => {
      const body = JSON.parse(String(init?.body)) as { schedule: { cron: string } }
      if (body.schedule.cron === '0 6 * * *') {
        // The first request answers late, after the user changed the field.
        return new Promise<Response>((resolve) => (release = resolve))
      }
      const hour = body.schedule.cron.split(' ')[1]
      return json({ runs: [`2026-11-09T0${hour}:00:00Z`], local: [], timezone: 'UTC' })
    })
    const { wrapper, form } = await mountFields(fetch, '0 5 * * *')
    const runs = () => wrapper.findAll('li').map((li) => li.text())
    const busy = () => wrapper.find('ul').attributes('aria-busy')
    await vi.advanceTimersByTimeAsync(450)
    expect(runs()[0]).toContain('05:00')
    form.value = { ...form.value, cron: '0 6 * * *' }
    await vi.advanceTimersByTimeAsync(450)
    expect(fetch).toHaveBeenCalledTimes(2)
    // The user edits again; the slow answer for 06:00 arrives before the request for 07:00 starts.
    form.value = { ...form.value, cron: '0 7 * * *' }
    await vi.advanceTimersByTimeAsync(100)
    release(json({ runs: ['2026-11-09T06:00:00Z'], local: [], timezone: 'UTC' }))
    await vi.advanceTimersByTimeAsync(10)
    expect(runs()[0]).toContain('05:00')
    expect(busy()).toBe('true')
    await vi.advanceTimersByTimeAsync(400)
    expect(runs()).toHaveLength(1)
    expect(runs()[0]).toContain('07:00')
    expect(busy()).toBe('false')
  })

  it('shows what is wrong under the cron field and asks for no preview', async () => {
    vi.useFakeTimers()
    const fetch = vi.fn<typeof globalThis.fetch>(async () => json({}))
    const { wrapper, form } = await mountFields(fetch, '61 * * * *')
    await vi.advanceTimersByTimeAsync(450)
    const cronInput = wrapper
      .findAll('input')
      .find((i) => (i.element as HTMLInputElement).value === '61 * * * *')
    expect(cronInput?.attributes('aria-invalid')).toBe('true')
    const errorId = cronInput?.attributes('aria-describedby')?.split(' ')[0] ?? ''
    expect(wrapper.find(`[id="${errorId}"]`).text()).toBe(
      'In the minute field, 61 is outside 0–59.',
    )
    form.value = { ...form.value, cron: '0 6 31 2 *' }
    // The old error goes at once; the new one comes when typing stops.
    await vi.advanceTimersByTimeAsync(10)
    expect(wrapper.text()).not.toContain('61 is outside')
    expect(wrapper.text()).not.toContain('never runs')
    await vi.advanceTimersByTimeAsync(450)
    expect(wrapper.text()).toContain('This schedule never runs')
    expect(fetch).not.toHaveBeenCalled()
  })
})
