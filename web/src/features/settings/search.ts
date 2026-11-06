/**
 * Settings search: ranks every setting by its name first, then its keywords and section name, then its
 * description. Names match fuzzily (in the active language and in English); keywords and section names only by
 * whole word or word start, and descriptions only as a substring, so long texts do not produce scattered-letter
 * noise or outrank a setting whose own name matches.
 */
import { bestScore, fuzzyScore, normalizeText } from '@/lib/fuzzy'
import { resolveText } from '@/stores/commands'

import { SECTIONS, type SectionId } from './sections'

/** fuzzyScore of a word-start match (see lib/fuzzy.ts). */
const WORD_START = 2000
const SECONDARY_WEIGHT = 0.5
const DESC_SCORE = 200

export interface SettingHit {
  section: SectionId
  item: string
  label: string
  score: number
}

function wordScore(query: string, texts: readonly string[]): number {
  let best = 0
  for (const text of texts) {
    const score = fuzzyScore(query, text)
    if (score >= WORD_START && score > best) best = score
  }
  return best * SECONDARY_WEIGHT
}

export function searchSettings(query: string): SettingHit[] {
  const trimmed = query.trim()
  if (!trimmed) return []
  const needle = normalizeText(trimmed)
  const hits: SettingHit[] = []
  for (const section of SECTIONS) {
    const sectionKey = { key: `settings.sections.${section.id}.label` }
    for (const item of section.items) {
      const base = `settings.items.${item}`
      const label = resolveText({ key: `${base}.label` })
      let score = Math.max(
        bestScore(trimmed, [
          [label, 1],
          [resolveText({ key: `${base}.label` }, 'en'), 0.9],
        ]),
        wordScore(trimmed, [
          resolveText({ key: `${base}.keywords` }),
          resolveText({ key: `${base}.keywords` }, 'en'),
          resolveText(sectionKey),
          resolveText(sectionKey, 'en'),
        ]),
      )
      if (score === 0 && normalizeText(resolveText({ key: `${base}.desc` })).includes(needle)) {
        score = DESC_SCORE
      }
      if (score > 0) hits.push({ section: section.id, item, label, score })
    }
  }
  return hits.sort((a, b) => b.score - a.score)
}
