import { describe, expect, it } from 'vitest'

import { MAX_PRICE_ROWS, priceErrorKey, priceRow, pricesInput } from './prices'

const row = (key: number, model: string, input: string, output: string, cached = '') => ({
  key,
  model,
  input,
  cached,
  output,
})

describe('pricesInput', () => {
  it('turns rows into the request, a comma as the decimal point and an empty cached input left out', () => {
    const { prices, errors } = pricesInput([
      row(1, ' deepseek-chat ', '0,27', '1.1', '0.07'),
      row(2, '*', '1', '2'),
    ])
    expect(errors).toEqual({})
    expect(prices).toEqual([
      { model: 'deepseek-chat', inputUsd: 0.27, outputUsd: 1.1, cachedInputUsd: 0.07 },
      { model: '*', inputUsd: 1, outputUsd: 2 },
    ])
    expect(pricesInput([])).toEqual({ prices: [], errors: {} })
  })

  it('names each field it refuses, as the daemon would', () => {
    const { errors } = pricesInput([
      row(1, '', '-1', 'abc', 'x'),
      row(2, 'GLM-4', '10001', '0'),
      row(3, 'glm-4', '1', '1'),
      row(4, 'm'.repeat(201), '.5', '1'),
    ])
    expect(errors).toEqual({
      '0.model': 'modelRequired',
      '0.input': 'invalid',
      '0.output': 'invalid',
      '0.cached': 'invalid',
      '1.input': 'invalid',
      '2.model': 'duplicate',
      '3.model': 'modelTooLong',
    })
  })

  it('refuses more rows than the daemon keeps', () => {
    const rows = Array.from({ length: MAX_PRICE_ROWS + 1 }, (_, i) => row(i, `m${i}`, '1', '1'))
    expect(pricesInput(rows).errors.table).toBe('tooMany')
  })

  it('fills a row from a saved price', () => {
    expect(priceRow(7, { model: 'x', inputUsd: 3, outputUsd: 15, cachedInputUsd: 0.3 })).toEqual(
      row(7, 'x', '3', '15', '0.3'),
    )
    expect(priceRow(8)).toEqual(row(8, '', '', ''))
  })
})

describe('priceErrorKey', () => {
  it('maps the daemon’s fields to the form’s', () => {
    expect(priceErrorKey('prices[2].outputUsd')).toBe('2.output')
    expect(priceErrorKey('prices[0].cachedInputUsd')).toBe('0.cached')
    expect(priceErrorKey('prices[1].model')).toBe('1.model')
    expect(priceErrorKey('prices')).toBe('table')
    expect(priceErrorKey('prices[1].other')).toBeNull()
    expect(priceErrorKey('name')).toBeNull()
  })
})
