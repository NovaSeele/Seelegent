/**
 * Memory amounts as people read them, one formatter for every view (banner, settings, terminal, attention): MB
 * from the daemon shown in GB from 1 GB up and in TB from 1 TB up, in the reader's number format ("850 MB",
 * "1.5 GB", "1 TB"). An unknown reading has no text.
 */
import type { MemoryStatus } from '@/api/types'
import type { Formatters } from '@/composables/useFormat'

const GB = 1024
const TB = 1024 * 1024

export function formatMB(fmt: Pick<Formatters, 'number'>, mb: number): string {
  const unit = (value: number, name: string, digits: number) =>
    fmt.number(value, { style: 'unit', unit: name, maximumFractionDigits: digits })
  if (mb >= TB) return unit(mb / TB, 'terabyte', 1)
  if (mb >= GB) return unit(mb / GB, 'gigabyte', 1)
  return unit(mb, 'megabyte', 0)
}

/** formatMB bound to a formatter, for code that only takes a function (attention text, the transcript). */
export type FormatMB = (mb: number) => string

export const mbFormatter =
  (fmt: Pick<Formatters, 'number'>): FormatMB =>
  (mb) =>
    formatMB(fmt, mb)

/** The free amount of one kind of a reading in MB, or null when the system gave none (commit outside Windows). */
export function freeMB(
  memory: MemoryStatus | null | undefined,
  kind: 'phys' | 'commit',
): number | null {
  if (!memory?.known) return null
  const mb = kind === 'phys' ? memory.availPhysMB : memory.availCommitMB
  return mb > 0 ? mb : null
}
