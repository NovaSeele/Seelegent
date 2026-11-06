/**
 * Typed, namespaced access to localStorage for per-viewer UI preferences.
 *
 * Every preference has its own key `seelegent.v1.<name>` holding a JSON value; no key is reused for two things.
 * Every read and write is wrapped in try/catch (private windows, blocked storage, quota) and validated, so a
 * missing, corrupt or foreign value falls back to the default instead of breaking the page.
 */
export const STORAGE_PREFIX = 'seelegent.v1.'

export interface StoredValue<T> {
  /** Name below the prefix, e.g. `ui.theme`. */
  readonly name: string
  readonly fallback: T
  /** Returns true when a parsed value has the right shape and range. */
  readonly validate: (value: unknown) => value is T
}

export function storageKey(name: string): string {
  return STORAGE_PREFIX + name
}

function getStorage(): Storage | null {
  try {
    return typeof window === 'undefined' ? null : window.localStorage
  } catch {
    return null
  }
}

export function readStored<T>(spec: StoredValue<T>): T {
  const storage = getStorage()
  if (!storage) return spec.fallback
  try {
    const raw = storage.getItem(storageKey(spec.name))
    if (raw === null) return spec.fallback
    const parsed: unknown = JSON.parse(raw)
    return spec.validate(parsed) ? parsed : spec.fallback
  } catch {
    return spec.fallback
  }
}

/** Saves a value; returns false when storage refused it (the in-memory state still applies). */
export function writeStored<T>(spec: StoredValue<T>, value: T): boolean {
  const storage = getStorage()
  if (!storage) return false
  try {
    storage.setItem(storageKey(spec.name), JSON.stringify(value))
    return true
  } catch {
    return false
  }
}

export function removeStored(spec: StoredValue<unknown>): void {
  try {
    getStorage()?.removeItem(storageKey(spec.name))
  } catch {
    // Storage unavailable: nothing to remove.
  }
}

/** Raw access for one-time migrations of the old dashboard's keys. */
export function readLegacy(key: string): string | null {
  try {
    return getStorage()?.getItem(key) ?? null
  } catch {
    return null
  }
}

export function removeLegacy(key: string): void {
  try {
    getStorage()?.removeItem(key)
  } catch {
    // ignore
  }
}

// Small validators shared by the preference definitions.
export const isOneOf =
  <T extends string>(values: readonly T[]) =>
  (value: unknown): value is T =>
    typeof value === 'string' && (values as readonly string[]).includes(value)

export const isNumberIn =
  (min: number, max: number) =>
  (value: unknown): value is number =>
    typeof value === 'number' && Number.isFinite(value) && value >= min && value <= max

export const isBoolean = (value: unknown): value is boolean => typeof value === 'boolean'
