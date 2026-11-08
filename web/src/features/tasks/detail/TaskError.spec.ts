import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import type { Attention } from '@/api/types'
import { flush, setupApp } from '@/test/app'
import { iso } from '@/test/fixtures'

import TaskError from './TaskError.vue'

const error = 'agy exited with code 0 and no result'
const failed = (params?: Attention['params']): Attention => ({
  state: 'blocked',
  reason: 'failed',
  who: 'lead',
  since: iso(),
  detail: error,
  params,
})

async function render(attention?: Attention) {
  const { plugins } = await setupApp()
  const wrapper = mount(TaskError, { props: { error, attention }, global: { plugins } })
  await flush()
  return wrapper
}

describe('TaskError (design review)', () => {
  it('keeps a coded failure behind the disclosure: the banner already words it', async () => {
    const wrapper = await render(failed({ kind: 'no_result', backend: 'agy', exitCode: 0 }))
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.find('details').exists()).toBe(true)
    expect(wrapper.find('details').attributes('open')).toBeUndefined()
    expect(wrapper.find('details code').text()).toBe(error)
  })

  it('shows any other error as the main text', async () => {
    for (const attention of [failed(), undefined]) {
      const wrapper = await render(attention)
      expect(wrapper.find('[role="alert"]').text()).toContain(error)
      expect(wrapper.find('details').exists()).toBe(false)
    }
  })
})
