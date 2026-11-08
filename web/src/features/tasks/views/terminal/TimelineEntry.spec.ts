import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import { setupApp } from '@/test/app'
import { makeEvent } from '@/test/fixtures'

import { buildTimeline, type TimelineItem } from './timeline'
import TimelineEntry from './TimelineEntry.vue'

async function render(item: TimelineItem) {
  const { plugins } = await setupApp()
  return mount(TimelineEntry, {
    props: { item, taskId: 't1', cwd: 'D:/app', expanded: false, briefs: new Map() },
    global: { plugins },
  })
}

const attention = (seq: number, data: Record<string, unknown>) =>
  makeEvent('t1', seq, { who: 'lead', since: '2026-11-06T12:00:00Z', ...data }, 'attention')

describe('TimelineEntry notices', () => {
  it('names a flag by its reason, with the files still at risk, and without daemon prose', async () => {
    const items = buildTimeline([
      makeEvent('t1', 1, { files: ['a.txt', 'b.txt'] }, 'work_at_risk'),
      makeEvent('t1', 2, { files: ['a.txt'] }, 'restored'),
      attention(3, { state: 'blocked', reason: 'work_at_risk', detail: '1 file(s) at risk' }),
    ])
    const restored = await render(items[1] as TimelineItem)
    expect(restored.text()).toContain('Restored 1 file (turn 1)')
    expect(restored.find('.te__notice--ok').exists()).toBe(true)

    const flag = await render(items[2] as TimelineItem)
    expect(flag.text()).toContain('Work may be lost · 1 file still at risk')
    expect(flag.text()).not.toContain('file(s)')
  })

  it('falls back to the state for a reason it does not know, and dims a note', async () => {
    const items = buildTimeline([
      attention(1, { state: 'blocked', reason: 'brand_new_reason', detail: 'something' }),
      attention(2, { state: 'info', reason: 'review' }),
    ])
    const unknown = await render(items[0] as TimelineItem)
    expect(unknown.text()).toBe('The worker is blocked')
    const note = await render(items[1] as TimelineItem)
    expect(note.text()).toBe('Awaiting review')
    expect(note.find('.te__notice--dim').exists()).toBe(true)
  })

  it('words a recovery notice from its code, never with the daemon English', async () => {
    const english = 'the mask list could not be read'
    const items = buildTimeline([
      makeEvent(
        't1',
        1,
        { accountId: 'a1', noticeCode: 'mask_unreadable', notice: english },
        'reattach',
      ),
      makeEvent('t1', 2, { accountId: 'a1', noticeCode: 'brand_new', notice: english }, 'resumed'),
    ])
    const known = await render(items[0] as TimelineItem)
    expect(known.text()).toContain('Reconnected to the running worker')
    expect(known.text()).toContain('also masked by key patterns')
    expect(known.find('.te__notice--warn').exists()).toBe(true)
    const unknown = await render(items[1] as TimelineItem)
    expect(unknown.text()).toContain('this dashboard does not know')
    for (const w of [known, unknown]) expect(w.text()).not.toContain(english)
  })

  it('names the steps of a gate in the reader language, with their commands', async () => {
    const items = buildTimeline([
      makeEvent(
        't1',
        1,
        {
          phase: 'start',
          steps: ['lane', 'hygiene', 'npm test'],
          plan: [
            { kind: 'lane', label: 'lane' },
            { kind: 'hygiene', label: 'hygiene' },
            { kind: 'task', label: 'npm test', cmd: 'npm test' },
          ],
        },
        'gate',
      ),
    ])
    const start = await render(items[0] as TimelineItem)
    expect(start.text()).toContain('Gate started: 3 steps')
    expect(start.find('pre').text()).toBe('Lane\nHygiene\nCommand: npm test')
  })
})
