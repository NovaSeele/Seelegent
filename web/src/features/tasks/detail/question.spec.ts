import { describe, expect, it } from 'vitest'

import { parseQuestion, reasonKey } from './question'

describe('worker question', () => {
  it('splits numbered choices from the question text', () => {
    const parsed = parseQuestion(
      'The old URL is still linked.\nWhat should happen?\n1. Redirect with 301\n2) Keep both\n',
    )
    expect(parsed.text).toBe('The old URL is still linked.\nWhat should happen?')
    expect(parsed.options).toEqual([
      { n: 1, label: 'Redirect with 301' },
      { n: 2, label: 'Keep both' },
    ])
  })

  it('keeps numbers that do not count up from 1 as text', () => {
    const parsed = parseQuestion('2. not a choice\n1. first\n3. skipped')
    expect(parsed.options).toEqual([{ n: 1, label: 'first' }])
    expect(parsed.text).toBe('2. not a choice\n3. skipped')
  })

  it('handles a missing detail', () => {
    expect(parseQuestion(undefined)).toEqual({ text: '', options: [] })
  })

  it('maps snake_case reasons to label keys', () => {
    expect(reasonKey('needs_lead_input')).toBe('needsLeadInput')
    expect(reasonKey('stalled')).toBe('stalled')
  })
})
