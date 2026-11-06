import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import { createAppI18n } from '@/i18n'

import HomePage from './HomePage.vue'

describe('HomePage', () => {
  it('renders the localized app title', () => {
    const wrapper = mount(HomePage, {
      global: { plugins: [createAppI18n('vi')] },
    })

    expect(wrapper.get('h1').text()).toBe('Seelegent')
  })
})
