/**
 * Multi-selection of list rows (click toggles, Shift extends from the anchor, Ctrl/Cmd+A takes every visible
 * row) and the bulk runner that applies one action to the chosen tasks a few at a time.
 */
export interface Selection {
  readonly ids: ReadonlySet<string>
  /** The last row toggled on its own: the start of a Shift range. */
  readonly anchor: string | null
}

export const EMPTY_SELECTION: Selection = { ids: new Set(), anchor: null }

export function toggle(selection: Selection, id: string): Selection {
  const ids = new Set(selection.ids)
  if (ids.has(id)) ids.delete(id)
  else ids.add(id)
  return { ids, anchor: id }
}

/** Adds every row between the anchor and `id` (in list order); without an anchor it toggles `id`. */
export function extendTo(selection: Selection, order: readonly string[], id: string): Selection {
  const from = selection.anchor ? order.indexOf(selection.anchor) : -1
  const to = order.indexOf(id)
  if (from < 0 || to < 0) return toggle(selection, id)
  const ids = new Set(selection.ids)
  for (let i = Math.min(from, to); i <= Math.max(from, to); i++) {
    const row = order[i]
    if (row !== undefined) ids.add(row)
  }
  return { ids, anchor: selection.anchor }
}

export function selectAll(order: readonly string[]): Selection {
  return { ids: new Set(order), anchor: order[0] ?? null }
}

/** Keeps only rows still listed (a filter change or a deleted task drops the rest). */
export function prune(selection: Selection, order: readonly string[]): Selection {
  const visible = new Set(order)
  const ids = [...selection.ids].filter((id) => visible.has(id))
  if (ids.length === selection.ids.size) return selection
  const anchor = selection.anchor && visible.has(selection.anchor) ? selection.anchor : null
  return { ids: new Set(ids), anchor }
}

export interface BulkResult {
  ok: string[]
  failed: { id: string; error: string }[]
}

/** Runs `action` for each id, at most `concurrency` at once; one failure never stops the others. */
export async function runBulk(
  ids: readonly string[],
  action: (id: string) => Promise<unknown>,
  concurrency = 3,
): Promise<BulkResult> {
  const result: BulkResult = { ok: [], failed: [] }
  const queue = [...ids]
  const worker = async () => {
    for (let id = queue.shift(); id !== undefined; id = queue.shift()) {
      try {
        await action(id)
        result.ok.push(id)
      } catch (error) {
        result.failed.push({ id, error: error instanceof Error ? error.message : String(error) })
      }
    }
  }
  await Promise.all(Array.from({ length: Math.max(1, Math.min(concurrency, ids.length)) }, worker))
  // Report in the order the user saw.
  const rank = new Map(ids.map((id, i) => [id, i]))
  result.ok.sort((a, b) => (rank.get(a) ?? 0) - (rank.get(b) ?? 0))
  result.failed.sort((a, b) => (rank.get(a.id) ?? 0) - (rank.get(b.id) ?? 0))
  return result
}
