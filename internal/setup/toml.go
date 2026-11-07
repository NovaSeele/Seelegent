package setup

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// This file is a small line-based TOML reader for Codex's config.toml. It is not a full TOML parser: it finds table
// headers and key/value lines (following values over several lines: arrays, inline tables, multi-line strings) so
// that setup can change exactly the lines of the [mcp_servers.seelegent] table and keep every other byte. It reads
// the values of that table (strings, arrays of strings, inline tables of strings) to tell whose entry it is.

// lineKind classifies a TOML line.
type lineKind int

const (
	lineBlank lineKind = iota
	lineComment
	lineHeader
	lineKey  // first line of a key/value pair
	lineCont // a later line of a multi-line value
)

// tline is one line of a TOML file.
type tline struct {
	text string // without the line end
	eol  string // "\n", "\r\n", or "" for a last line without one
	kind lineKind
	// path is the key path of a header or of a key/value line.
	path []string
	// arrayTable marks a [[header]].
	arrayTable bool
	// valueAt is the byte offset where the value starts (lineKey).
	valueAt int
	// owner is the index of the lineKey line a lineCont belongs to.
	owner int
}

// tomlFile is a parsed config.toml.
type tomlFile struct {
	lines []tline
	bom   bool
	eol   string // the file's line end, for new lines
}

// scanState tracks a value that continues on the next line.
type scanState struct {
	depth     int    // open [ and {
	multiline string // `"""` or `'''` while inside a multi-line string
}

func (s scanState) open() bool { return s.depth > 0 || s.multiline != "" }

// parseTOML splits text into classified lines.
func parseTOML(text string) (*tomlFile, error) {
	f := &tomlFile{eol: "\n"}
	if strings.HasPrefix(text, utf8BOM) {
		f.bom, text = true, text[len(utf8BOM):]
	}
	if i := strings.IndexByte(text, '\n'); i > 0 && text[i-1] == '\r' {
		f.eol = "\r\n"
	}
	var st scanState
	owner := -1
	for n := 0; text != ""; n++ {
		line, eol := text, ""
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			line, eol, text = text[:i], "\n", text[i+1:]
			if strings.HasSuffix(line, "\r") {
				line, eol = line[:len(line)-1], "\r\n"
			}
		} else {
			text = ""
		}
		l := tline{text: line, eol: eol, owner: -1}
		if st.open() {
			l.kind, l.owner = lineCont, owner
			if err := scanValue(line, 0, &st); err != nil {
				return nil, fmt.Errorf("line %d: %w", n+1, err)
			}
			f.lines = append(f.lines, l)
			continue
		}
		trimmed := strings.TrimLeft(line, " \t")
		switch {
		case trimmed == "":
			l.kind = lineBlank
		case trimmed[0] == '#':
			l.kind = lineComment
		case trimmed[0] == '[':
			path, array, err := parseHeader(trimmed)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", n+1, err)
			}
			l.kind, l.path, l.arrayTable = lineHeader, path, array
		default:
			off := len(line) - len(trimmed)
			path, next, err := parseKeyPath(line, off)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", n+1, err)
			}
			next = skipWS(line, next)
			if next >= len(line) || line[next] != '=' {
				return nil, fmt.Errorf("line %d: expected = after the key", n+1)
			}
			l.kind, l.path, l.valueAt = lineKey, path, skipWS(line, next+1)
			owner = len(f.lines)
			if err := scanValue(line, l.valueAt, &st); err != nil {
				return nil, fmt.Errorf("line %d: %w", n+1, err)
			}
		}
		f.lines = append(f.lines, l)
	}
	if st.open() {
		return nil, errors.New("the file ends inside a value")
	}
	return f, nil
}

func skipWS(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

// parseHeader reads "[a.b]" or "[[a.b]]" with an optional comment after it.
func parseHeader(s string) (path []string, array bool, err error) {
	i := 1
	if strings.HasPrefix(s, "[[") {
		array, i = true, 2
	}
	path, i, err = parseKeyPath(s, skipWS(s, i))
	if err != nil {
		return nil, false, err
	}
	i = skipWS(s, i)
	closing := "]"
	if array {
		closing = "]]"
	}
	if !strings.HasPrefix(s[i:], closing) {
		return nil, false, errors.New("malformed table header")
	}
	rest := strings.TrimSpace(s[i+len(closing):])
	if rest != "" && rest[0] != '#' {
		return nil, false, errors.New("unexpected text after a table header")
	}
	return path, array, nil
}

// parseKeyPath reads a dotted key (bare, "basic" or 'literal' parts) starting at i.
func parseKeyPath(s string, i int) (path []string, next int, err error) {
	for {
		i = skipWS(s, i)
		if i >= len(s) {
			return nil, i, errors.New("missing key")
		}
		var part string
		switch s[i] {
		case '"':
			str, after, err := readBasic(s, i+1)
			if err != nil {
				return nil, i, err
			}
			part, i = str, after
		case '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return nil, i, errors.New("unterminated quoted key")
			}
			part, i = s[i+1:i+1+end], i+end+2
		default:
			start := i
			for i < len(s) && isBareKeyChar(s[i]) {
				i++
			}
			if i == start {
				return nil, i, fmt.Errorf("unexpected %q in a key", s[i])
			}
			part = s[start:i]
		}
		path = append(path, part)
		j := skipWS(s, i)
		if j < len(s) && s[j] == '.' {
			i = j + 1
			continue
		}
		return path, i, nil
	}
}

func isBareKeyChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

// readBasic reads a "basic string" body starting after the opening quote and returns the decoded value and the
// index after the closing quote.
func readBasic(s string, i int) (str string, next int, err error) {
	var b strings.Builder
	for i < len(s) {
		c := s[i]
		switch c {
		case '"':
			return b.String(), i + 1, nil
		case '\\':
			r, n, err := readEscape(s, i)
			if err != nil {
				return "", i, err
			}
			b.WriteString(r)
			i += n
			continue
		}
		b.WriteByte(c)
		i++
	}
	return "", i, errors.New("unterminated string")
}

// readEscape decodes the escape at s[i] (a backslash) and returns its text and length.
func readEscape(s string, i int) (text string, size int, err error) {
	if i+1 >= len(s) {
		return "", 0, errors.New("unterminated escape")
	}
	switch s[i+1] {
	case 'b':
		return "\b", 2, nil
	case 't':
		return "\t", 2, nil
	case 'n':
		return "\n", 2, nil
	case 'f':
		return "\f", 2, nil
	case 'r':
		return "\r", 2, nil
	case 'e':
		return "\x1b", 2, nil
	case '"':
		return `"`, 2, nil
	case '\\':
		return `\`, 2, nil
	case 'u', 'U':
		n := 4
		if s[i+1] == 'U' {
			n = 8
		}
		if i+2+n > len(s) {
			return "", 0, errors.New("short unicode escape")
		}
		v, err := strconv.ParseInt(s[i+2:i+2+n], 16, 32) // at most 0x7fffffff: fits a rune
		if err != nil || !utf8.ValidRune(rune(v)) {
			return "", 0, errors.New("bad unicode escape")
		}
		return string(rune(v)), 2 + n, nil
	}
	return "", 0, fmt.Errorf("unknown escape \\%c", s[i+1])
}

// scanValue walks a value's text from i, tracking brackets, braces and multi-line strings that continue on the
// next line. A comment ends the scan.
func scanValue(s string, i int, st *scanState) error {
	for i < len(s) {
		if st.multiline != "" {
			end := strings.Index(s[i:], st.multiline)
			if st.multiline == `"""` {
				end = indexUnescaped(s[i:], `"""`)
			}
			if end < 0 {
				return nil
			}
			i += end + 3
			for i < len(s) && s[i] == st.multiline[0] { // up to two quotes may belong to the string
				i++
			}
			st.multiline = ""
			continue
		}
		c := s[i]
		switch {
		case strings.HasPrefix(s[i:], `"""`), strings.HasPrefix(s[i:], `'''`):
			st.multiline = s[i : i+3]
			i += 3
		case c == '"':
			_, next, err := readBasic(s, i+1)
			if err != nil {
				return err
			}
			i = next
		case c == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return errors.New("unterminated string")
			}
			i += end + 2
		case c == '#':
			return nil
		case c == '[' || c == '{':
			st.depth++
			i++
		case c == ']' || c == '}':
			st.depth--
			if st.depth < 0 {
				return errors.New("unbalanced brackets")
			}
			i++
		default:
			i++
		}
	}
	return nil
}

// indexUnescaped finds sub in s, skipping backslash escapes.
func indexUnescaped(s, sub string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if strings.HasPrefix(s[i:], sub) {
			return i
		}
	}
	return -1
}

// tval is a parsed TOML value: a string, an array, an inline table, or something else (numbers, booleans, dates)
// kept as raw text.
type tval struct {
	str    *string
	arr    []tval
	isArr  bool
	table  []tkv
	isTbl  bool
	raw    string
	isMore bool // a value of another type
}

// tkv is one key of an inline table.
type tkv struct {
	key []string
	val tval
}

// strings returns the value as a list of strings, if it is one.
func (v tval) strings() ([]string, bool) {
	if !v.isArr {
		return nil, false
	}
	out := make([]string, 0, len(v.arr))
	for _, e := range v.arr {
		if e.str == nil {
			return nil, false
		}
		out = append(out, *e.str)
	}
	return out, true
}

// stringMap returns an inline table of strings as a map, if it is one.
func (v tval) stringMap() (map[string]string, bool) {
	if !v.isTbl {
		return nil, false
	}
	out := map[string]string{}
	for _, kv := range v.table {
		if len(kv.key) != 1 || kv.val.str == nil {
			return nil, false
		}
		out[kv.key[0]] = *kv.val.str
	}
	return out, true
}

// valueParser reads one value from text that may span several lines.
type valueParser struct {
	s string
	i int
}

// parseValue parses the whole value text (comments allowed after it and, in arrays, between elements).
func parseValue(s string) (tval, error) {
	p := &valueParser{s: s}
	v, err := p.value()
	if err != nil {
		return tval{}, err
	}
	p.skip()
	if p.i < len(p.s) {
		return tval{}, fmt.Errorf("unexpected %q after the value", p.s[p.i:])
	}
	return v, nil
}

// skip skips white space, line ends and comments (the value text only has line ends where TOML allows them).
func (p *valueParser) skip() {
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ' ', '\t', '\r', '\n':
			p.i++
		case '#':
			for p.i < len(p.s) && p.s[p.i] != '\n' {
				p.i++
			}
		default:
			return
		}
	}
}

func (p *valueParser) value() (tval, error) {
	if p.i >= len(p.s) {
		return tval{}, errors.New("missing value")
	}
	switch {
	case strings.HasPrefix(p.s[p.i:], `"""`):
		end := indexUnescaped(p.s[p.i+3:], `"""`)
		if end < 0 {
			return tval{}, errors.New("unterminated string")
		}
		body := p.s[p.i+3 : p.i+3+end]
		p.i += 3 + end + 3
		body = strings.TrimPrefix(strings.TrimPrefix(body, "\r"), "\n")
		str, err := decodeMultiBasic(body)
		return tval{str: &str}, err
	case strings.HasPrefix(p.s[p.i:], `'''`):
		end := strings.Index(p.s[p.i+3:], `'''`)
		if end < 0 {
			return tval{}, errors.New("unterminated string")
		}
		str := strings.TrimPrefix(strings.TrimPrefix(p.s[p.i+3:p.i+3+end], "\r"), "\n")
		p.i += 3 + end + 3
		return tval{str: &str}, nil
	case p.s[p.i] == '"':
		str, next, err := readBasic(p.s, p.i+1)
		if err != nil {
			return tval{}, err
		}
		p.i = next
		return tval{str: &str}, nil
	case p.s[p.i] == '\'':
		end := strings.IndexByte(p.s[p.i+1:], '\'')
		if end < 0 {
			return tval{}, errors.New("unterminated string")
		}
		str := p.s[p.i+1 : p.i+1+end]
		p.i += end + 2
		return tval{str: &str}, nil
	case p.s[p.i] == '[':
		return p.array()
	case p.s[p.i] == '{':
		return p.inlineTable()
	}
	start := p.i
	for p.i < len(p.s) && !strings.ContainsRune(",]} \t\r\n#", rune(p.s[p.i])) {
		p.i++
	}
	if p.i == start {
		return tval{}, fmt.Errorf("unexpected %q", p.s[p.i])
	}
	return tval{raw: p.s[start:p.i], isMore: true}, nil
}

func (p *valueParser) array() (tval, error) {
	p.i++ // [
	v := tval{isArr: true}
	for {
		p.skip()
		if p.i >= len(p.s) {
			return tval{}, errors.New("unterminated array")
		}
		if p.s[p.i] == ']' {
			p.i++
			return v, nil
		}
		e, err := p.value()
		if err != nil {
			return tval{}, err
		}
		v.arr = append(v.arr, e)
		p.skip()
		if p.i < len(p.s) && p.s[p.i] == ',' {
			p.i++
			continue
		}
		if p.i < len(p.s) && p.s[p.i] == ']' {
			p.i++
			return v, nil
		}
		return tval{}, errors.New("expected , or ] in an array")
	}
}

func (p *valueParser) inlineTable() (tval, error) {
	p.i++ // {
	v := tval{isTbl: true}
	for {
		p.skip()
		if p.i >= len(p.s) {
			return tval{}, errors.New("unterminated inline table")
		}
		if p.s[p.i] == '}' {
			p.i++
			return v, nil
		}
		key, next, err := parseKeyPath(p.s, p.i)
		if err != nil {
			return tval{}, err
		}
		p.i = skipWS(p.s, next)
		if p.i >= len(p.s) || p.s[p.i] != '=' {
			return tval{}, errors.New("expected = in an inline table")
		}
		p.i++
		p.skip()
		e, err := p.value()
		if err != nil {
			return tval{}, err
		}
		v.table = append(v.table, tkv{key: key, val: e})
		p.skip()
		if p.i < len(p.s) && p.s[p.i] == ',' {
			p.i++
			continue
		}
		if p.i < len(p.s) && p.s[p.i] == '}' {
			p.i++
			return v, nil
		}
		return tval{}, errors.New("expected , or } in an inline table")
	}
}

// decodeMultiBasic decodes the body of a """multi-line basic string""" (escapes and line-ending backslashes).
func decodeMultiBasic(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			i++
			continue
		}
		j := i + 1
		for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
			j++
		}
		if j < len(s) && (s[j] == '\n' || s[j] == '\r') { // line-ending backslash: drop white space up to the text
			for j < len(s) && strings.IndexByte(" \t\r\n", s[j]) >= 0 {
				j++
			}
			i = j
			continue
		}
		r, n, err := readEscape(s, i)
		if err != nil {
			return "", err
		}
		b.WriteString(r)
		i += n
	}
	return b.String(), nil
}

// valueText returns the full text of the value of the key line at index k (continuation lines joined).
func (f *tomlFile) valueText(k int) string {
	var b strings.Builder
	b.WriteString(f.lines[k].text[f.lines[k].valueAt:])
	for j := k + 1; j < len(f.lines) && f.lines[j].kind == lineCont && f.lines[j].owner == k; j++ {
		b.WriteString("\n")
		b.WriteString(f.lines[j].text)
	}
	return b.String()
}

// valueEnd returns the index after the last line of the key/value pair starting at k.
func (f *tomlFile) valueEnd(k int) int {
	j := k + 1
	for j < len(f.lines) && f.lines[j].kind == lineCont && f.lines[j].owner == k {
		j++
	}
	return j
}

// render returns the file content.
func (f *tomlFile) render() string {
	var b strings.Builder
	if f.bom {
		b.WriteString(utf8BOM)
	}
	for _, l := range f.lines {
		b.WriteString(l.text)
		b.WriteString(l.eol)
	}
	return b.String()
}

// tomlString renders s as a TOML string: a 'literal string' when it can be one (Windows paths stay readable),
// else a "basic string" with escapes.
func tomlString(s string) string {
	literal := !strings.ContainsAny(s, "'\n\r")
	for _, r := range s {
		if r < 0x20 && r != '\t' || r == 0x7f {
			literal = false
		}
	}
	if literal {
		return "'" + s + "'"
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// tomlKey renders a key: bare when possible, else quoted.
func tomlKey(k string) string {
	if k != "" && strings.IndexFunc(k, func(r rune) bool { return r > 0x7f || !isBareKeyChar(byte(r&0x7f)) }) < 0 {
		return k
	}
	return tomlString(k)
}
