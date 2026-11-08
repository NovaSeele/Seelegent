/**
 * Free memory as people read it (Health.memory, resources.changed): MB from the daemon shown in GB from 1 GB up,
 * in the reader's number format ("12.3 GB", "850 MB"). An unknown reading has no text.
 */
import type { MemoryStatus } from '@/api/types'
import type { Formatters } from '@/composables/useFormat'

export function formatMB(fmt: Pick<Formatters, 'number'>, mb: number): string {
  if (mb >= 1024) {
    return fmt.number(mb / 1024, { style: 'unit', unit: 'gigabyte', maximumFractionDigits: 1 })
  }
  return fmt.number(mb, { style: 'unit', unit: 'megabyte', maximumFractionDigits: 0 })
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
