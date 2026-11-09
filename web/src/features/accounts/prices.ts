/**
 * The Prices form's rules: rows of text (model, input, cached input, cache write, output; USD per 1M tokens) turned
 * into the PUT /providers/{id}/prices body, checked as the daemon checks it (a model on every row, unique ignoring
 * case, at most 200 characters; prices 0..10000; cached input empty means the input price, cache write empty means
 * 1.25 × the input price; at most 200 rows), and the
 * daemon's field errors ("prices[2].outputUsd") mapped back to the row and column they are about.
 */
import type { Price, PriceInput } from '@/api/types'

export type PriceColumn = 'model' | 'input' | 'cached' | 'write' | 'output'
export type PriceProblem = 'modelRequired' | 'modelTooLong' | 'duplicate' | 'invalid' | 'tooMany'

export interface PriceRow {
  key: number
  model: string
  input: string
  cached: string
  write: string
  output: string
}

export const MAX_PRICE = 10_000
export const MAX_PRICE_ROWS = 200
const MAX_MODEL = 200

/** A row of the form; `key` tells rows apart for the list (the form counts them). */
export function priceRow(key: number, price?: Price): PriceRow {
  const num = (v: number | undefined) => (v === undefined ? '' : String(v))
  return {
    key,
    model: price?.model ?? '',
    input: num(price?.inputUsd),
    cached: num(price?.cachedInputUsd),
    write: num(price?.cacheWriteUsd),
    output: num(price?.outputUsd),
  }
}

/** "1.5", "1,5" and " 2 " read as numbers; anything else (or out of range) is not a price. */
export function parsePrice(text: string): number | null {
  const t = text.trim().replace(',', '.')
  if (!/^\d+(\.\d+)?$|^\.\d+$/.test(t)) return null
  const n = Number(t)
  return n >= 0 && n <= MAX_PRICE ? n : null
}

/** A five-minute cache write without its own price costs 1.25 × the input price (Anthropic's rule). */
export const CACHE_WRITE_FACTOR = 1.25

/** What an empty cache write stands for, as the form's placeholder: "" while the input is not a price. */
export function defaultCacheWrite(input: string): string {
  const n = parsePrice(input)
  return n === null ? '' : String(Math.round(n * CACHE_WRITE_FACTOR * 1e6) / 1e6)
}

/** Field errors are keyed "<row>.<column>"; "table" is about the whole table. */
export function pricesInput(rows: readonly PriceRow[]): {
  prices: PriceInput[]
  errors: Record<string, PriceProblem>
} {
  const errors: Record<string, PriceProblem> = {}
  const prices: PriceInput[] = []
  const seen = new Set<string>()
  if (rows.length > MAX_PRICE_ROWS) errors.table = 'tooMany'
  rows.forEach((row, i) => {
    const model = row.model.trim()
    if (!model) errors[`${i}.model`] = 'modelRequired'
    else if (model.length > MAX_MODEL) errors[`${i}.model`] = 'modelTooLong'
    else if (seen.has(model.toLowerCase())) errors[`${i}.model`] = 'duplicate'
    seen.add(model.toLowerCase())
    const input = parsePrice(row.input)
    const output = parsePrice(row.output)
    const optional = (text: string) => (text.trim() === '' ? undefined : parsePrice(text))
    const cached = optional(row.cached)
    const write = optional(row.write)
    if (input === null) errors[`${i}.input`] = 'invalid'
    if (output === null) errors[`${i}.output`] = 'invalid'
    if (cached === null) errors[`${i}.cached`] = 'invalid'
    if (write === null) errors[`${i}.write`] = 'invalid'
    if (input !== null && output !== null && cached !== null && write !== null)
      prices.push({
        model,
        inputUsd: input,
        outputUsd: output,
        ...(cached !== undefined ? { cachedInputUsd: cached } : {}),
        ...(write !== undefined ? { cacheWriteUsd: write } : {}),
      })
  })
  return { prices, errors }
}

const SERVER_COLUMN: Readonly<Record<string, PriceColumn>> = {
  model: 'model',
  inputUsd: 'input',
  cachedInputUsd: 'cached',
  cacheWriteUsd: 'write',
  outputUsd: 'output',
}

/** The form key of a daemon field ("prices[2].outputUsd" → "2.output", "prices" → "table"), or null. */
export function priceErrorKey(field: string): string | null {
  if (field === 'prices') return 'table'
  const m = /^prices\[(\d+)\]\.(\w+)$/.exec(field)
  const column = m?.[2] ? SERVER_COLUMN[m[2]] : undefined
  return m && column ? `${m[1]}.${column}` : null
}
