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

// `code`, **bold**, [text](url), bare http(s) URLs.
const INLINE =
  /(`+)([^`]|[^`][\s\S]*?[^`])\1(?!`)|\*\*([^*\n]+?)\*\*|\[([^\]\n]+)\]\((https?:\/\/[^\s)]+)\)|(https?:\/\/[^\s<>()]+[^\s<>().,;:!?'"])/g

export function parseInline(text: string): Inline[] {
  const out: Inline[] = []
  const pushText = (value: string) => {
    if (!value) return
    const last = out.at(-1)
    if (last?.t === 'text') last.v += value
    else out.push({ t: 'text', v: value })
  }
  let index = 0
  for (const match of text.matchAll(INLINE)) {
    const at = match.index ?? 0
    pushText(text.slice(index, at))
    index = at + match[0].length
    if (match[2] !== undefined) out.push({ t: 'code', v: match[2] })
    else if (match[3] !== undefined) out.push({ t: 'strong', c: parseInline(match[3]) })
    else if (match[4] !== undefined && match[5] !== undefined) {
      const href = safeHref(match[5])
      if (href) out.push({ t: 'link', href, c: parseInline(match[4]) })
      else pushText(match[0])
    } else if (match[6] !== undefined) {
      const href = safeHref(match[6])
      if (href) out.push({ t: 'link', href, c: [{ t: 'text', v: match[6] }] })
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
