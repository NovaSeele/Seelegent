import { mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'

import { flush, setupApp } from '@/test/app'

import Tooltip from './Tooltip.vue'

const tip = () => document.body.querySelector<HTMLElement>('[role="tooltip"]')

/** jsdom lays nothing out: give the anchor a real box (or none, for the 0×0 case). */
function boxAt(wrapper: VueWrapper, top: number, width = 40, height = 20) {
  vi.spyOn(wrapper.element as HTMLElement, 'getBoundingClientRect').mockReturnValue(
    new DOMRect(100, top, width, height),
  )
}

async function mountTip() {
  const { plugins } = await setupApp()
  return mount(Tooltip, {
    props: { text: 'Gemini Models · Weekly', delay: 450 },
    slots: { default: '<span class="bar">bar</span>' },
    attachTo: document.body,
    global: { plugins },
  })
}

async function hover(wrapper: VueWrapper, beforeTimer?: () => void) {
  vi.useFakeTimers()
  await wrapper.trigger('pointerenter')
  beforeTimer?.()
  vi.advanceTimersByTime(450)
  vi.useRealTimers()
}

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
  document.body.innerHTML = ''
})

describe('Tooltip', () => {
  it('is not visible before it has been positioned, then shows at its place', async () => {
    const wrapper = await mountTip()
    boxAt(wrapper, 300)
    await hover(wrapper)
    await nextTick()
    // Rendered (so it can be measured) but unseen: never a flash at the top-left corner.
    expect(tip()).not.toBeNull()
    expect(tip()?.style.visibility).toBe('hidden')
    await flush()
    expect(tip()?.style.visibility).toBe('')
    expect(tip()?.style.position).toBe('fixed')
    expect(tip()?.style.top).not.toBe('0px')
    wrapper.unmount()
  })

  it('never shows at 0,0 when its anchor left the document before the delay ran out', async () => {
    const wrapper = await mountTip()
    boxAt(wrapper, 300)
    // The row re-rendered under the resting pointer: the anchor is detached when the timer fires.
    await hover(wrapper, () => (wrapper.element as HTMLElement).remove())
    await flush()
    const shown = tip()
    expect(shown === null || shown.style.visibility === 'hidden').toBe(true)
    wrapper.unmount()
  })

  it('stays hidden and closes when its anchor has no box (display: none)', async () => {
    const wrapper = await mountTip()
    boxAt(wrapper, 0, 0, 0)
    await hover(wrapper)
    await flush()
    expect(tip()).toBeNull()
    wrapper.unmount()
  })

  it('hides when a scroll moves its trigger, not when something else scrolls', async () => {
    const wrapper = await mountTip()
    boxAt(wrapper, 300)
    await hover(wrapper)
    await flush()
    expect(tip()?.style.visibility).toBe('')

    window.dispatchEvent(new Event('scroll'))
    await flush()
    expect(tip()).not.toBeNull()

    boxAt(wrapper, 200)
    window.dispatchEvent(new Event('scroll'))
    await flush()
    expect(tip()).toBeNull()
    wrapper.unmount()
  })
})
