import { computed, type ComputedRef } from 'vue'

/**
 * A computed that keeps returning its previous value while `equals` says nothing changed, so components that
 * render it are not re-rendered. Lists of ids built from a store that changes on every event use it: one task
 * update recomputes the id list, but the list (and every row) re-renders only when ids, order or groups change.
 */
export function stableComputed<T>(
  getter: () => T,
  equals: (a: T, b: T) => boolean,
): ComputedRef<T> {
  return computed<T>((previous) => {
    const next = getter()
    return previous !== undefined && equals(previous, next) ? previous : next
  })
}

export function sameArray<T>(a: readonly T[], b: readonly T[]): boolean {
  if (a === b) return true
  if (a.length !== b.length) return false
  for (let i = 0; i < a.length; i++) if (!Object.is(a[i], b[i])) return false
  return true
}

/** Deep equality for plain JSON-like data (arrays, objects, primitives). */
export function sameJson(a: unknown, b: unknown): boolean {
  if (Object.is(a, b)) return true
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) return false
  if (Array.isArray(a) !== Array.isArray(b)) return false
  const ka = Object.keys(a)
  const kb = Object.keys(b)
  if (ka.length !== kb.length) return false
  return ka.every((key) =>
    sameJson((a as Record<string, unknown>)[key], (b as Record<string, unknown>)[key]),
  )
}
