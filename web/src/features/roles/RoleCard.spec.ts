import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import type { Role } from '@/api/types'
import Icon from '@/components/ui/Icon.vue'
import { setupApp } from '@/test/app'

import RoleCard from './RoleCard.vue'

function role(overrides: Partial<Role> = {}): Role {
  return {
    id: 'qa',
    name: 'QA',
    description: 'Checks the work.',
    rules: '',
    outputStyle: '',
    skills: [],
    preferredAccounts: [],
    scope: 'global',
    overridesGlobal: false,
    ...overrides,
  }
}

async function render(props: Partial<InstanceType<typeof RoleCard>['$props']> = {}) {
  const { plugins } = await setupApp()
  return mount(RoleCard, {
    props: { role: role(), projectName: '', ...props },
    global: { plugins },
  })
}

describe('RoleCard', () => {
  it('deletes with a trash button, not a close-looking ×', async () => {
    const wrapper = await render()
    const button = wrapper.get('button[aria-label="Delete QA"]')
    expect(button.findComponent(Icon).props('name')).toBe('trash')
  })
})
