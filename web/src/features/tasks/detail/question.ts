/**
 * A worker's question: the text in attention.detail and its choices in attention.params.options. A flag raised before
 * the options moved to the params lists them in the detail as numbered lines ("1. Redirect with 301"). The card
 * shows the text and one button per choice.
 */
export interface ParsedQuestion {
  text: string
  options: { n: number; label: string }[]
}

const OPTION = /^\s*(\d{1,2})[.)]\s+(.+?)\s*$/

export function parseQuestion(detail: string | undefined, choices?: string[]): ParsedQuestion {
  if (choices?.length) {
    return {
      text: (detail ?? '').trim(),
      options: choices.map((label, i) => ({ n: i + 1, label })),
    }
  }
  const text: string[] = []
  const options: ParsedQuestion['options'] = []
  for (const line of (detail ?? '').split('\n')) {
    const match = OPTION.exec(line)
    const n = match ? Number(match[1]) : NaN
    // Choices count up from 1 without gaps; anything else is part of the question.
    if (match?.[2] && n === options.length + 1) options.push({ n, label: match[2] })
    else text.push(line)
  }
  return { text: text.join('\n').trim(), options }
}

/** Attention reasons from the daemon (snake_case) to their label key under common.attention. */
export function reasonKey(reason: string): string {
  return reason.replace(/_([a-z])/g, (_, ch: string) => ch.toUpperCase())
}
