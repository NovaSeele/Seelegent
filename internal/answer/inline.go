package answer

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// StripInline turns one line of a member's markdown into plain text the way CommonMark reads its inline markup, so
// what is left is what the member meant to say:
//   - a code span loses its backticks and keeps everything inside literally (`CORS_ORIGINS`, `**x**`); an unclosed
//     backtick run is plain text;
//   - `*` and `_` disappear only where they open or close emphasis that another delimiter pairs with. An underscore
//     inside a word never does (CORS_ORIGINS, snake_case_name), and neither does a lone or space-surrounded `*`.
//
// The old reader removed every `*`, `_` and backtick, so a required change "set CORS_ORIGINS" read "set CORSORIGINS".
func StripInline(s string) string {
	if !strings.ContainsAny(s, "`*_") {
		return s
	}
	spans := codeSpans(s)
	delims := emphasisDelims(s, spans)
	matchEmphasis(delims)

	var b strings.Builder
	b.Grow(len(s))
	sp, dl := 0, 0
	for i := 0; i < len(s); {
		if sp < len(spans) && spans[sp].start == i {
			b.WriteString(spans[sp].code)
			i = spans[sp].end
			sp++
			continue
		}
		if dl < len(delims) && delims[dl].pos == i {
			d := delims[dl]
			b.WriteString(strings.Repeat(string(d.ch), d.left))
			i += d.orig
			dl++
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// codeSpan is a code span of s: its source range [start, end) and its literal content.
type codeSpan struct {
	start, end int
	code       string
}

// codeSpans finds the code spans of s: a backtick run closes at the next run of the same length; a run that nothing
// closes is plain text, and the search goes on after it.
func codeSpans(s string) []codeSpan {
	var out []codeSpan
	for i := 0; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		n := runLen(s, i, '`')
		end := -1
		for j := i + n; j < len(s); {
			if s[j] != '`' {
				j++
				continue
			}
			m := runLen(s, j, '`')
			if m == n {
				end = j
				break
			}
			j += m
		}
		if end < 0 {
			i += n
			continue
		}
		code := s[i+n : end]
		// CommonMark strips one space on each side when both are there and the content is not only spaces.
		if len(code) >= 2 && code[0] == ' ' && code[len(code)-1] == ' ' && strings.Trim(code, " ") != "" {
			code = code[1 : len(code)-1]
		}
		out = append(out, codeSpan{start: i, end: end + n, code: code})
		i = end + n
	}
	return out
}

// delim is a run of `*` or `_`: where it starts, its length, how many of its characters stay as text, and whether it
// can open or close emphasis.
type delim struct {
	pos, orig, left int
	ch              byte
	open, close     bool
}

// emphasisDelims lists the runs of `*` and `_` outside code spans, classified by CommonMark's flanking rules.
func emphasisDelims(s string, spans []codeSpan) []delim {
	var out []delim
	sp := 0
	for i := 0; i < len(s); {
		if sp < len(spans) && spans[sp].start == i {
			i = spans[sp].end
			sp++
			continue
		}
		c := s[i]
		if c != '*' && c != '_' {
			i++
			continue
		}
		n := runLen(s, i, c)
		before, after := ' ', ' ' // the start and the end of the line count as white space
		if i > 0 {
			before, _ = utf8.DecodeLastRuneInString(s[:i])
		}
		if i+n < len(s) {
			after, _ = utf8.DecodeRuneInString(s[i+n:])
		}
		left := !isSpace(after) && (!isPunct(after) || isSpace(before) || isPunct(before))
		right := !isSpace(before) && (!isPunct(before) || isSpace(after) || isPunct(after))
		d := delim{pos: i, orig: n, left: n, ch: c, open: left, close: right}
		if c == '_' {
			// An underscore inside a word is a letter, not emphasis.
			d.open = left && (!right || isPunct(before))
			d.close = right && (!left || isPunct(after))
		}
		out = append(out, d)
		i += n
	}
	return out
}

// matchEmphasis pairs closers with the nearest earlier opener of the same character (CommonMark's "process
// emphasis", including its rule of three); every paired character is removed from delims[i].left.
func matchEmphasis(ds []delim) {
	var stack []int
	for i := range ds {
		d := &ds[i]
		for d.close && d.left > 0 {
			at := -1
			for k := len(stack) - 1; k >= 0; k-- {
				o := &ds[stack[k]]
				if o.ch != d.ch || o.left == 0 {
					continue
				}
				if (o.open && o.close || d.open && d.close) && (o.orig+d.orig)%3 == 0 &&
					(o.orig%3 != 0 || d.orig%3 != 0) {
					continue
				}
				at = k
				break
			}
			if at < 0 {
				break
			}
			o := &ds[stack[at]]
			use := 1
			if o.left >= 2 && d.left >= 2 {
				use = 2
			}
			o.left -= use
			d.left -= use
			// The delimiters between the pair can no longer open anything.
			stack = stack[:at+1]
			if o.left == 0 {
				stack = stack[:at]
			}
		}
		if d.open && d.left > 0 {
			stack = append(stack, i)
		}
	}
}

func runLen(s string, i int, c byte) int {
	j := i
	for j < len(s) && s[j] == c {
		j++
	}
	return j - i
}

func isSpace(r rune) bool { return unicode.IsSpace(r) }

// isPunct is CommonMark's Unicode punctuation: the P and S categories.
func isPunct(r rune) bool { return unicode.IsPunct(r) || unicode.IsSymbol(r) }
