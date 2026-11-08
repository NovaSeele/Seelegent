/**
 * The Prices form's rules: rows of text (model, input, cached input, output; USD per 1M tokens) turned into the
 * PUT /providers/{id}/prices body, checked as the daemon checks it (a model on every row, unique ignoring case, at
 * most 200 characters; prices 0..10000; cached input empty means the input price; at most 200 rows), and the
 * daemon's field errors ("prices[2].outputUsd") mapped back to the row and column they are about.
 */
import type { Price, PriceInput } from '@/api/types'

export type PriceColumn = 'model' | 'input' | 'cached' | 'output'
export type PriceProblem = 'modelRequired' | 'modelTooLong' | 'duplicate' | 'invalid' | 'tooMany'

export interface PriceRow {
  key: number
  model: string
  input: string
  cached: string
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
    output: num(price?.outputUsd),
  }
}

/** "1.5", "1,5" and " 2 " read as numbers; anything else (or out of range) is not a price. */
function parsePrice(text: string): number | null {
  const t = text.trim().replace(',', '.')
  if (!/^\d+(\.\d+)?$|^\.\d+$/.test(t)) return null
  const n = Number(t)
  return n >= 0 && n <= MAX_PRICE ? n : null
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
    const cached = row.cached.trim() === '' ? undefined : parsePrice(row.cached)
    if (input === null) errors[`${i}.input`] = 'invalid'
    if (output === null) errors[`${i}.output`] = 'invalid'
    if (cached === null) errors[`${i}.cached`] = 'invalid'
    if (input !== null && output !== null && cached !== null)
      prices.push({
        model,
        inputUsd: input,
        outputUsd: output,
        ...(cached !== undefined ? { cachedInputUsd: cached } : {}),
      })
  })
  return { prices, errors }
}

const SERVER_COLUMN: Readonly<Record<string, PriceColumn>> = {
  model: 'model',
  inputUsd: 'input',
  cachedInputUsd: 'cached',
  outputUsd: 'output',
}

/** The form key of a daemon field ("prices[2].outputUsd" → "2.output", "prices" → "table"), or null. */
export function priceErrorKey(field: string): string | null {
  if (field === 'prices') return 'table'
  const m = /^prices\[(\d+)\]\.(\w+)$/.exec(field)
  const column = m?.[2] ? SERVER_COLUMN[m[2]] : undefined
  return m && column ? `${m[1]}.${column}` : null
}
