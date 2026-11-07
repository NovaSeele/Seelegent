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
  it('marks a chosen skill that is not installed where the role lives', async () => {
    const wrapper = await render({
      role: role({ skills: ['vue-review', 'gone-skill'] }),
      installed: new Set(['vue-review']),
    })
    const missing = wrapper.findAll('.skill--missing')
    expect(missing).toHaveLength(1)
    expect(missing[0]?.text()).toContain('gone-skill')
    expect(missing[0]?.classes()).toContain('tone-attention')
    expect(missing[0]?.attributes('title')).toBe('Not installed here')
    // Unknown (not read yet): nothing is marked.
    const unknown = await render({ role: role({ skills: ['gone-skill'] }), installed: null })
    expect(unknown.findAll('.skill--missing')).toHaveLength(0)
  })

  it('deletes with a trash button, not a close-looking ×', async () => {
    const wrapper = await render()
    const button = wrapper.get('button[aria-label="Delete QA"]')
    expect(button.findComponent(Icon).props('name')).toBe('trash')
  })
})
