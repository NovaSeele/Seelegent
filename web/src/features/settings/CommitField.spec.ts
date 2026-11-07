import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import CommitField from './CommitField.vue'

// Live run against the daemon: Settings › Backends showed every allowed-models and tier field empty although the
// daemon had values, and typing in them saved nothing (v-model on <component :is="'input'"> bound a modelValue
// attribute instead of the input's value).
describe('CommitField', () => {
  it.each([false, true])(
    'shows the saved value and commits what is typed (multiline %s)',
    async (multiline) => {
      const wrapper = mount(CommitField, {
        props: { modelValue: '^gemini-3\\.8-', label: 'Allowed models', multiline },
      })
      const field = wrapper.find<HTMLInputElement | HTMLTextAreaElement>(
        multiline ? 'textarea' : 'input',
      )
      expect(field.element.value).toBe('^gemini-3\\.8-')

      await field.trigger('focus')
      await field.setValue('^claude-')
      await field.trigger('blur')
      expect(wrapper.emitted('commit')).toEqual([['^claude-']])

      await wrapper.setProps({ modelValue: '^claude-' })
      expect(field.element.value).toBe('^claude-')
    },
  )

  it('does not commit an unchanged value', async () => {
    const wrapper = mount(CommitField, { props: { modelValue: 'a', label: 'Field' } })
    const input = wrapper.find('input')
    await input.trigger('focus')
    await input.trigger('blur')
    expect(wrapper.emitted('commit')).toBeUndefined()
    expect(wrapper.emitted('unchanged')).toHaveLength(1)
  })

  // Live run: Esc after a refused regex put the saved value back but kept the daemon's error under it.
  it('says unchanged when Esc puts the saved value back, so the form can drop the error', async () => {
    const wrapper = mount(CommitField, {
      props: {
        modelValue: '',
        label: 'Allowed models',
        error: 'is not a valid regular expression',
      },
    })
    const input = wrapper.find('input')
    await input.trigger('focus')
    await input.setValue('claude-(')
    await input.trigger('keydown', { key: 'Escape' })
    expect(input.element.value).toBe('')
    expect(wrapper.emitted('unchanged')).toHaveLength(1)
    expect(wrapper.emitted('commit')).toBeUndefined()
  })
})
