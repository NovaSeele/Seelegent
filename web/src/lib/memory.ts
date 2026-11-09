/**
 * Memory amounts as people read them, one formatter for every view (banner, settings, terminal, attention): MB
 * from the daemon shown in GB from 1 GB up and in TB from 1 TB up, in the reader's number format ("850 MB",
 * "1.5 GB", "1 TB"). An unknown reading has no text. Amounts shown side by side that would round to the same text
 * ("2 GB free, needs 2 GB" for 2040 and 2048 MB) are shown in MB instead (distinctMB).
 */
import type { MemoryStatus } from '@/api/types'
import type { Formatters } from '@/composables/useFormat'

const GB = 1024
const TB = 1024 * 1024

/** `exact`: always in MB, unrounded to GB. */
export function formatMB(fmt: Pick<Formatters, 'number'>, mb: number, exact = false): string {
  const unit = (value: number, name: string, digits: number) =>
    fmt.number(value, { style: 'unit', unit: name, maximumFractionDigits: digits })
  if (exact) return unit(mb, 'megabyte', 0)
  if (mb >= TB) return unit(mb / TB, 'terabyte', 1)
  if (mb >= GB) return unit(mb / GB, 'gigabyte', 1)
  return unit(mb, 'megabyte', 0)
}

/** A file size in bytes: bytes and KB below 1 MB, then as formatMB reads it ("512 bytes", "12 KB", "1.5 GB"). */
export function formatBytes(fmt: Pick<Formatters, 'number'>, bytes: number): string {
  const unit = (value: number, name: string) =>
    fmt.number(value, { style: 'unit', unit: name, unitDisplay: 'short', maximumFractionDigits: 0 })
  if (bytes < 1024) return unit(bytes, 'byte')
  if (bytes < 1024 * 1024) return unit(bytes / 1024, 'kilobyte')
  return formatMB(fmt, bytes / (1024 * 1024))
}

/** formatMB bound to a formatter, for code that only takes a function (attention text, the transcript). */
export type FormatMB = (mb: number, exact?: boolean) => string

export const mbFormatter =
  (fmt: Pick<Formatters, 'number'>): FormatMB =>
  (mb, exact) =>
    formatMB(fmt, mb, exact)

/**
 * Amounts shown together: each as `format` reads it, unless two different amounts would read the same, then all
 * of them in MB, so a reader never sees "2 GB free, needs 2 GB" for a task that is held.
 */
export function distinctMB(format: FormatMB, values: readonly number[]): string[] {
  const texts = values.map((mb) => format(mb))
  const clash = texts.some((text, i) =>
    texts.some((other, j) => other === text && values[j] !== values[i]),
  )
  return clash ? values.map((mb) => format(mb, true)) : texts
}

/** The free amount of one kind of a reading in MB, or null when the system gave none (commit outside Windows). */
export function freeMB(
  memory: MemoryStatus | null | undefined,
  kind: 'phys' | 'commit',
): number | null {
  if (!memory?.known) return null
  const mb = kind === 'phys' ? memory.availPhysMB : memory.availCommitMB
  return mb > 0 ? mb : null
}
