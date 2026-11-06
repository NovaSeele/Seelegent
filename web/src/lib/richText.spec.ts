import { describe, expect, it } from 'vitest'

import { parseInline, parseRichText, safeHref } from './richText'

describe('rich text', () => {
  it('only allows http(s) links', () => {
    expect(safeHref('https://example.com/a?b=1')).toBe('https://example.com/a?b=1')
    expect(safeHref('javascript:alert(1)')).toBeNull()
    expect(safeHref('data:text/html,<b>x</b>')).toBeNull()
    expect(safeHref('not a url')).toBeNull()
  })

  it('parses code, bold, links and bare URLs, leaving the rest as text', () => {
    expect(
      parseInline('run `go test` **now**, see [docs](https://go.dev) or https://x.dev/a.'),
    ).toEqual([
      { t: 'text', v: 'run ' },
      { t: 'code', v: 'go test' },
      { t: 'text', v: ' ' },
      { t: 'strong', c: [{ t: 'text', v: 'now' }] },
      { t: 'text', v: ', see ' },
      { t: 'link', href: 'https://go.dev/', c: [{ t: 'text', v: 'docs' }] },
      { t: 'text', v: ' or ' },
      { t: 'link', href: 'https://x.dev/a', c: [{ t: 'text', v: 'https://x.dev/a' }] },
      { t: 'text', v: '.' },
    ])
  })

  it('never turns markup or unsafe links into anything but text', () => {
    expect(parseInline('<img src=x onerror=alert(1)> [x](javascript:alert(1))')).toEqual([
      { t: 'text', v: '<img src=x onerror=alert(1)> [x](javascript:alert(1))' },
    ])
  })

  it('parses blocks: headings, lists, fenced code and paragraphs with line breaks', () => {
    const blocks = parseRichText(
      '## Done\r\nChanged two files.\nAll tests pass.\n\n- one\n- two\n\n3. third\n4. fourth\n```go\nfunc main() {}\n\n```\ntail',
    )
    expect(blocks).toEqual([
      { t: 'h', level: 2, c: [{ t: 'text', v: 'Done' }] },
      {
        t: 'p',
        c: [
          { t: 'text', v: 'Changed two files.' },
          { t: 'br' },
          { t: 'text', v: 'All tests pass.' },
        ],
      },
      { t: 'ul', items: [[{ t: 'text', v: 'one' }], [{ t: 'text', v: 'two' }]] },
      { t: 'ol', start: 3, items: [[{ t: 'text', v: 'third' }], [{ t: 'text', v: 'fourth' }]] },
      { t: 'code', lang: 'go', v: 'func main() {}\n' },
      { t: 'p', c: [{ t: 'text', v: 'tail' }] },
    ])
  })

  it('keeps an unclosed fence as code to the end', () => {
    expect(parseRichText('```\nx\ny')).toEqual([{ t: 'code', lang: '', v: 'x\ny' }])
  })
})
