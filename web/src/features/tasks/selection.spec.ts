import { describe, expect, it } from 'vitest'

import { EMPTY_SELECTION, extendTo, prune, runBulk, selectAll, toggle } from './selection'

const order = ['a', 'b', 'c', 'd', 'e']
const ids = (s: { ids: ReadonlySet<string> }) => [...s.ids].sort()

describe('selection', () => {
  it('toggles one row and remembers it as the anchor', () => {
    const one = toggle(EMPTY_SELECTION, 'b')
    expect(ids(one)).toEqual(['b'])
    expect(one.anchor).toBe('b')
    expect(ids(toggle(one, 'b'))).toEqual([])
  })

  it('extends from the anchor in list order, either direction', () => {
    const start = toggle(EMPTY_SELECTION, 'd')
    expect(ids(extendTo(start, order, 'b'))).toEqual(['b', 'c', 'd'])
    expect(ids(extendTo(toggle(EMPTY_SELECTION, 'b'), order, 'e'))).toEqual(['b', 'c', 'd', 'e'])
    // No anchor: behaves like a toggle.
    expect(ids(extendTo(EMPTY_SELECTION, order, 'c'))).toEqual(['c'])
  })

  it('selects all visible rows and prunes rows that left the list', () => {
    const all = selectAll(order)
    expect(all.ids.size).toBe(5)
    const pruned = prune(all, ['b', 'c'])
    expect(ids(pruned)).toEqual(['b', 'c'])
    expect(pruned.anchor).toBeNull()
    // Unchanged selections keep their identity, so watchers do not fire.
    expect(prune(pruned, order)).toBe(pruned)
  })
})

describe('bulk runner', () => {
  it('runs a few at a time, keeps going after failures and reports in list order', async () => {
    let running = 0
    let peak = 0
    const result = await runBulk(
      order,
      async (id) => {
        running++
        peak = Math.max(peak, running)
        await new Promise((resolve) => setTimeout(resolve, id === 'a' ? 5 : 1))
        running--
        if (id === 'b' || id === 'd') throw new Error(`no ${id}`)
      },
      2,
    )
    expect(peak).toBe(2)
    expect(result.ok).toEqual(['a', 'c', 'e'])
    expect(result.failed).toEqual([
      { id: 'b', error: 'no b' },
      { id: 'd', error: 'no d' },
    ])
  })

  it('does nothing for an empty list', async () => {
    expect(await runBulk([], () => Promise.resolve())).toEqual({ ok: [], failed: [] })
  })
})
