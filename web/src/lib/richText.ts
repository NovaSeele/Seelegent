/**
 * A small Markdown subset for worker and lead text, parsed into plain data that RichText.vue turns into VNodes
 * (never HTML strings, never v-html): paragraphs with line breaks, headings, bullet and numbered lists, fenced
 * code blocks, inline `code`, **bold**, and links. Only http(s) links become anchors; anything else stays text.
 */
export type Inline =
  | { t: 'text'; v: string }
  | { t: 'code'; v: string }
  | { t: 'strong'; c: Inline[] }
  | { t: 'link'; href: string; c: Inline[] }
  | { t: 'br' }

export type Block =
  | { t: 'p'; c: Inline[] }
  | { t: 'h'; level: 1 | 2 | 3; c: Inline[] }
  | { t: 'code'; lang: string; v: string }
  | { t: 'ul'; items: Inline[][] }
  | { t: 'ol'; start: number; items: Inline[][] }

const FENCE = /^\s*(```|~~~)\s*([\w+#.-]*)\s*$/
const BULLET = /^\s*[-*+]\s+(.*)$/
const NUMBERED = /^\s*(\d{1,4})[.)]\s+(.*)$/
const HEADING = /^(#{1,6})\s+(.*)$/

export function safeHref(href: string): string | null {
  try {
    const url = new URL(href)
    return url.protocol === 'http:' || url.protocol === 'https:' ? url.href : null
  } catch {
    return null
  }
}

interface CodeSpan {
  start: number
  end: number
  code: string
}

/**
 * Code spans in one linear pass: each backtick run closes at the next run of the same length (CommonMark's rule),
 * found through one forward-only cursor per run length. A regex for this backtracks quadratically on unclosed
 * runs, which a worker's output can easily contain.
 */
function codeSpans(text: string): CodeSpan[] {
  const runs: { start: number; len: number }[] = []
  for (let i = text.indexOf('`'); i >= 0; i = text.indexOf('`', i)) {
    let j = i
    while (text.charCodeAt(j) === 96) j++
    runs.push({ start: i, len: j - i })
    i = j
  }
  const byLength = new Map<number, number[]>()
  runs.forEach((run, k) => {
    const list = byLength.get(run.len)
    if (list) list.push(k)
    else byLength.set(run.len, [k])
  })
  const cursor = new Map<number, number>()
  const spans: CodeSpan[] = []
  for (let k = 0; k < runs.length;) {
    const open = runs[k] as { start: number; len: number }
    const list = byLength.get(open.len) ?? []
    let p = cursor.get(open.len) ?? 0
    while (p < list.length && (list[p] ?? 0) <= k) p++
    cursor.set(open.len, p)
    const closeAt = list[p]
    const close = closeAt === undefined ? undefined : runs[closeAt]
    if (closeAt === undefined || !close) {
      k++
      continue
    }
    spans.push({
      start: open.start,
      end: close.start + close.len,
      code: text.slice(open.start + open.len, close.start),
    })
    cursor.set(open.len, p + 1)
    k = closeAt + 1
  }
  return spans
}

// **bold**, [text](url), bare http(s) URLs. Every repetition stops at a delimiter it cannot cross, so a failed
// attempt costs at most the distance to that delimiter; link text is capped to keep "[[[[..." lines linear.
const INLINE =
  /\*\*([^*\n]+?)\*\*|\[([^\]\n[]{1,500})\]\((https?:\/\/[^\s)]+)\)|(https?:\/\/[^\s<>()]+[^\s<>().,;:!?'"])/g

export function parseInline(text: string): Inline[] {
  const out: Inline[] = []
  const pushText = (value: string) => {
    if (!value) return
    const last = out.at(-1)
    if (last?.t === 'text') last.v += value
    else out.push({ t: 'text', v: value })
  }
  const spans = codeSpans(text)
  let next = 0
  // The leftmost other match from `index` on; reused while it still lies ahead, so the text is scanned once.
  const pattern = new RegExp(INLINE.source, 'g')
  let match: RegExpExecArray | null = null
  let done = false
  let index = 0
  for (;;) {
    while (next < spans.length && (spans[next]?.start ?? 0) < index) next++
    const span = spans[next]
    if (!done && (!match || match.index < index)) {
      pattern.lastIndex = index
      match = pattern.exec(text)
      // No match from here on means none further on either: never scan the rest again.
      done = match === null
    }
    if (span && (!match || span.start <= match.index)) {
      pushText(text.slice(index, span.start))
      out.push({ t: 'code', v: span.code })
      index = span.end
      continue
    }
    if (!match) break
    pushText(text.slice(index, match.index))
    index = match.index + match[0].length
    if (match[1] !== undefined) out.push({ t: 'strong', c: parseInline(match[1]) })
    else if (match[2] !== undefined && match[3] !== undefined) {
      const href = safeHref(match[3])
      if (href) out.push({ t: 'link', href, c: parseInline(match[2]) })
      else pushText(match[0])
    } else if (match[4] !== undefined) {
      const href = safeHref(match[4])
      if (href) out.push({ t: 'link', href, c: [{ t: 'text', v: match[4] }] })
      else pushText(match[0])
    }
  }
  pushText(text.slice(index))
  return out
}

function paragraph(lines: string[]): Inline[] {
  const out: Inline[] = []
  lines.forEach((line, i) => {
    if (i > 0) out.push({ t: 'br' })
    out.push(...parseInline(line))
  })
  return out
}

export function parseRichText(source: string): Block[] {
  const lines = source.replace(/\r\n?/g, '\n').split('\n')
  const blocks: Block[] = []
  let para: string[] = []
  const flush = () => {
    if (para.length) blocks.push({ t: 'p', c: paragraph(para) })
    para = []
  }
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i] ?? ''
    const fence = FENCE.exec(line)
    if (fence) {
      flush()
      const body: string[] = []
      let j = i + 1
      while (j < lines.length && !(lines[j] ?? '').trim().startsWith(fence[1] ?? '```')) {
        body.push(lines[j] ?? '')
        j++
      }
      blocks.push({ t: 'code', lang: fence[2] ?? '', v: body.join('\n') })
      i = j
      continue
    }
    if (!line.trim()) {
      flush()
      continue
    }
    const heading = HEADING.exec(line)
    if (heading) {
      flush()
      const level = Math.min(3, heading[1]?.length ?? 1) as 1 | 2 | 3
      blocks.push({ t: 'h', level, c: parseInline(heading[2] ?? '') })
      continue
    }
    const bullet = BULLET.exec(line)
    const numbered = bullet ? null : NUMBERED.exec(line)
    if (bullet || numbered) {
      flush()
      const ordered = !!numbered
      const items: Inline[][] = []
      let j = i
      for (; j < lines.length; j++) {
        const item = ordered ? NUMBERED.exec(lines[j] ?? '') : BULLET.exec(lines[j] ?? '')
        if (!item) break
        items.push(parseInline((ordered ? item[2] : item[1]) ?? ''))
      }
      i = j - 1
      blocks.push(
        ordered ? { t: 'ol', start: Number(numbered?.[1] ?? 1), items } : { t: 'ul', items },
      )
      continue
    }
    para.push(line)
  }
  flush()
  return blocks
}
