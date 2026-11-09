import { mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'

import type { Account } from '@/api/types'
import { useAccountsStore } from '@/stores/accounts'
import { setupApp } from '@/test/app'
import { makeAccount, makeAccountFleet } from '@/test/fixtures'

import AccountList from './AccountList.vue'
import AccountRow from './AccountRow.vue'
import { BACKENDS } from './accountView'

let wrapper: VueWrapper | undefined

async function render(list: Account[]) {
  const { plugins } = await setupApp()
  useAccountsStore().replaceAll(list)
  const groups = BACKENDS.map((backend) => ({
    backend,
    items: list.filter((a) => a.backend === backend),
  })).filter((g) => g.items.length)
  wrapper = mount(AccountList, {
    props: { groups, now: Date.now() },
    global: { plugins },
    attachTo: document.body,
  })
  return wrapper
}

afterEach(() => wrapper?.unmount())

const rows = (w: VueWrapper) => w.findAll('li.row')
const chevron = (w: VueWrapper, i: number) => rows(w)[i]?.find('.row__chev')
const openRows = (w: VueWrapper) => w.findAll('.row__details').length

describe('AccountList', () => {
  const three = () => [
    makeAccount({ id: 'a1', label: 'One' }),
    makeAccount({ id: 'a2', label: 'Two' }),
    makeAccount({ id: 'a3', label: 'Three', backend: 'claude' }),
  ]

  it('groups by backend under a heading with the provider mark, name and count', async () => {
    const w = await render(three())
    const headings = w.findAll('h2')
    expect(headings.map((h) => h.text())).toEqual(['Antigravity 2', 'Claude Code 1'])
    expect(headings[0]?.find('.pm').attributes('data-backend')).toBe('agy')
    // Each row carries its backend's 3px edge.
    expect(rows(w)[2]?.attributes('style')).toContain('--provider-edge: var(--provider-claude)')
  })

  it('opens a row on click and on its chevron, one row at a time', async () => {
    const w = await render(three())
    expect(openRows(w)).toBe(0)
    await rows(w)[0]?.find('.row__main').trigger('click')
    expect(openRows(w)).toBe(1)
    expect(chevron(w, 0)?.attributes('aria-expanded')).toBe('true')

    // The chevron is the keyboard way in (Enter / Space click a button); opening another closes the first.
    await chevron(w, 1)?.trigger('click')
    expect(openRows(w)).toBe(1)
    expect(chevron(w, 0)?.attributes('aria-expanded')).toBe('false')
    expect(chevron(w, 1)?.attributes('aria-expanded')).toBe('true')
    expect(rows(w)[1]?.find('.row__details').exists()).toBe(true)
    expect(chevron(w, 1)?.attributes('aria-controls')).toBe(
      rows(w)[1]?.find('.row__details').attributes('id'),
    )

    await chevron(w, 1)?.trigger('click')
    expect(openRows(w)).toBe(0)
  })

  it('shows the full quota groups and facts when open, as the card does', async () => {
    const account = makeAccount({
      id: 'a1',
      busy: 1,
      quota: {
        source: 'active',
        groups: [{ name: 'Gemini Models', windows: [{ window: '5h', remaining: 0.5 }] }],
      },
    })
    const w = await render([account])
    await rows(w)[0]?.find('.row__main').trigger('click')
    const details = rows(w)[0]?.find('.row__details')
    expect(details?.text()).toContain('1 of 2 slots busy')
    expect(details?.find('.quota__name').text()).toBe('Gemini Models')
  })

  it('does not open the row from the ⋯ menu, the switch or the priority field', async () => {
    const w = await render(three())
    const row = rows(w)[0]
    await row?.find('[aria-haspopup="menu"]').trigger('click')
    await row?.find('[role="switch"]').trigger('click')
    await row?.find('.row__priority input').trigger('click')
    expect(openRows(w)).toBe(0)
    // The menu did open.
    expect(document.body.querySelector('[role="menu"]')).not.toBeNull()
  })

  it('lets 20 accounts be scanned without opening any: status, slots and quota on every row', async () => {
    const fleet = makeAccountFleet(20)
    const w = await render(fleet)
    expect(rows(w)).toHaveLength(20)
    expect(openRows(w)).toBe(0)
    for (const row of rows(w)) {
      expect(row.find('.pill').text()).not.toBe('')
      // A sign-in row shows its quota; an API-key row its spend in the same column.
      expect(row.find('.row__quota').text()).not.toBe('')
    }
    // Antigravity rows show the Gemini group, never the drained Claude/GPT or Argon groups.
    const agyRows = rows(w).filter((r) => r.attributes('style')?.includes('provider-agy'))
    for (const row of agyRows) expect(row.find('.qb').text()).not.toContain('Argon')
  })
})

describe('AccountRow, narrow', () => {
  it('moves the priority into the ⋯ menu', async () => {
    const { plugins } = await setupApp()
    const account = makeAccount({ priority: 7 })
    useAccountsStore().replaceAll([account])
    const props = { account, now: Date.now(), open: false, narrow: true }
    wrapper = mount(AccountRow, { props, global: { plugins }, attachTo: document.body })
    expect(wrapper.find('.row__priority').exists()).toBe(false)
    await wrapper.find('[aria-haspopup="menu"]').trigger('click')
    const items = [...document.body.querySelectorAll('[role="menuitem"]')].map(
      (el) => el.textContent,
    )
    expect(items.some((text) => text?.includes('Priority: 7…'))).toBe(true)

    await wrapper.setProps({ narrow: false })
    expect(wrapper.find('.row__priority').exists()).toBe(true)
  })
})
