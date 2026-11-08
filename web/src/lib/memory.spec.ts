import { describe, expect, it } from 'vitest'

import { createFormatters } from '@/composables/useFormat'

import { distinctMB, formatMB, freeMB, mbFormatter } from './memory'

const en = createFormatters('en')

describe('memory amounts', () => {
  it('reads MB below 1 GB, GB from 1 GB and TB from 1 TB, or MB when asked', () => {
    expect(formatMB(en, 850)).toBe('850 MB')
    expect(formatMB(en, 1536)).toBe('1.5 GB')
    expect(formatMB(en, 1024 * 1024)).toBe('1 TB')
    expect(formatMB(en, 2040, true)).toBe('2,040 MB')
  })

  it('shows amounts read together in MB when two different ones would read the same', () => {
    const mb = mbFormatter(en)
    expect(distinctMB(mb, [1536, 2048])).toEqual(['1.5 GB', '2 GB'])
    expect(distinctMB(mb, [2040, 2048])).toEqual(['2,040 MB', '2,048 MB'])
    expect(distinctMB(mb, [2048, 2048])).toEqual(['2 GB', '2 GB'])
    expect(distinctMB(mb, [])).toEqual([])
  })

  it('gives no free amount for an unknown reading', () => {
    expect(freeMB({ availPhysMB: 900, availCommitMB: 0, known: false }, 'phys')).toBeNull()
    expect(freeMB({ availPhysMB: 900, availCommitMB: 0, known: true }, 'phys')).toBe(900)
  })
})
