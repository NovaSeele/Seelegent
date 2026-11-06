/**
 * Fuzzy matching for the command palette and settings search. Case-insensitive and diacritic-insensitive:
 * "tien trinh" finds "Tiến trình", "dong" finds "Đóng". Score 0 means no match; higher is better
 * (exact > prefix > whole word > word prefix > substring > in-order characters). Ported from the old
 * dashboard's commands.js with the same ranking.
 */
export function normalizeText(text: string): string {
  return text.normalize('NFD').replace(/[̀-ͯ]/g, '').replace(/[đĐ]/g, 'd').toLowerCase().trim()
}

export function fuzzyScore(query: string, text: string): number {
  const q = normalizeText(query)
  const t = normalizeText(text)
  if (!q || !t) return 0
  if (t === q) return 10000
  if (t.startsWith(q)) return 5000 + Math.max(0, 100 - (t.length - q.length))

  const words = t.split(/[\s\-_/:.]+/).filter(Boolean)
  for (let i = 0; i < words.length; i++) {
    const word = words[i] as string
    if (word === q) return 3000 + (10 - Math.min(i, 9))
    if (word.startsWith(q)) return 2000 + (10 - Math.min(i, 9))
  }

  const at = t.indexOf(q)
  if (at >= 0) return Math.max(500, 1000 - at * 5)

  // Characters of the query in order, rewarding runs and word starts.
  let score = 0
  let from = 0
  let previous = -2
  let run = 0
  let first = -1
  for (const char of q) {
    if (char === ' ') continue
    const found = t.indexOf(char, from)
    if (found < 0) return 0
    if (first < 0) first = found
    score += 10
    if (found === previous + 1) {
      run++
      score += run * 15
    } else run = 0
    if (found === 0 || /[\s\-_/:.]/.test(t[found - 1] ?? '')) score += 25
    previous = found
    from = found + 1
  }
  return Math.max(1, Math.min(499, score - (previous - first)))
}

/** Best score of `query` against several texts, each with a weight (title 1, keywords lower). */
export function bestScore(query: string, texts: readonly (readonly [string, number])[]): number {
  let best = 0
  for (const [text, weight] of texts) {
    const score = fuzzyScore(query, text) * weight
    if (score > best) best = score
  }
  return best
}
