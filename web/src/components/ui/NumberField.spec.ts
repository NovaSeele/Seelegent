import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import NumberField from './NumberField.vue'

const make = () => mount(NumberField, { props: { modelValue: 2, label: 'Tasks', unit: 'min' } })

describe('NumberField', () => {
  it('commits the parsed value once for Enter followed by blur', async () => {
    const wrapper = make()
    const input = wrapper.get('input')
    await input.setValue('0')
    await input.trigger('keydown', { key: 'Enter' })
    await input.trigger('blur')
    expect(wrapper.emitted('commit')).toEqual([[0]])
    await input.setValue('3,5')
    await input.trigger('blur')
    expect(wrapper.emitted('commit')).toEqual([[0], [3.5]])
  })

  it('rounds a whole-number field, shows the rounded value and sends it once', async () => {
    const wrapper = mount(NumberField, { props: { modelValue: 2, label: 'Tasks', integer: true } })
    const input = wrapper.get('input')
    await input.setValue('4.6')
    await input.trigger('keydown', { key: 'Enter' })
    expect(input.element.value).toBe('5')
    // The parent saves it: the value comes back, then the field loses focus.
    await wrapper.setProps({ modelValue: 5 })
    await input.trigger('blur')
    expect(wrapper.emitted('commit')).toEqual([[5]])
    // Rounds to the value it already has: nothing to send.
    await input.setValue('5.2')
    await input.trigger('blur')
    expect(input.element.value).toBe('5')
    expect(wrapper.emitted('commit')).toEqual([[5]])
  })

  it('puts the value back on Esc and for text that is not a number', async () => {
    const wrapper = make()
    const input = wrapper.get('input')
    await input.setValue('9')
    await input.trigger('keydown', { key: 'Escape' })
    expect(input.element.value).toBe('2')
    await input.setValue('abc')
    await input.trigger('blur')
    expect(input.element.value).toBe('2')
    expect(wrapper.emitted('commit')).toBeUndefined()
  })
})
