package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Splicing: the document's text is scanned for the byte ranges of members and list elements, and an edit replaces
// exactly one such range (a value, a whole member with its comma, or the inside of an empty container). After each
// edit the text is parsed again, so a mistake can never reach the file.

// jpath addresses a value in a JSON document: string elements are object keys, int elements are list indexes.
type jpath []any

// errScan is a document the scanner cannot follow; it parsed before, so this is a bug, never user input.
var errScan = errors.New("cannot scan the JSON text")

// jspan is one member of an object or one element of a list in the text.
type jspan struct {
	// start is the first byte of the member's key, or of the element's value.
	start int
	key   string
	// keyEnd is the byte after the key's closing quote (members only).
	keyEnd       int
	vstart, vend int
}

// jcontainer is an object or a list in the text.
type jcontainer struct {
	open, close int // the brackets
	list        bool
	kids        []jspan
}

func isJSONSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

func jskipSpace(s []byte, i int) int {
	for i < len(s) && isJSONSpace(s[i]) {
		i++
	}
	return i
}

// jscanString returns the byte after the string that starts at s[i] (a quote).
func jscanString(s []byte, i int) (int, error) {
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case '"':
			return j + 1, nil
		}
	}
	return 0, errScan
}

// jscanValue returns the byte after the value that starts at s[i].
func jscanValue(s []byte, i int) (int, error) {
	if i >= len(s) {
		return 0, errScan
	}
	switch s[i] {
	case '"':
		return jscanString(s, i)
	case '{', '[':
		depth := 0
		for j := i; j < len(s); j++ {
			switch s[j] {
			case '"':
				end, err := jscanString(s, j)
				if err != nil {
					return 0, err
				}
				j = end - 1
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return j + 1, nil
				}
			}
		}
		return 0, errScan
	}
	j := i
	for j < len(s) && !isJSONSpace(s[j]) && s[j] != ',' && s[j] != '}' && s[j] != ']' {
		j++
	}
	if j == i {
		return 0, errScan
	}
	return j, nil
}

// jscanContainer reads the object or list whose opening bracket is s[open].
func jscanContainer(s []byte, open int) (jcontainer, error) {
	c := jcontainer{open: open, list: s[open] == '['}
	end := byte('}')
	if c.list {
		end = ']'
	}
	i := jskipSpace(s, open+1)
	if i < len(s) && s[i] == end {
		c.close = i
		return c, nil
	}
	for {
		k := jspan{start: i}
		if !c.list {
			if i >= len(s) || s[i] != '"' {
				return c, errScan
			}
			ke, err := jscanString(s, i)
			if err != nil {
				return c, err
			}
			if err := json.Unmarshal(s[i:ke], &k.key); err != nil {
				return c, errScan
			}
			k.keyEnd = ke
			i = jskipSpace(s, ke)
			if i >= len(s) || s[i] != ':' {
				return c, errScan
			}
			i = jskipSpace(s, i+1)
		}
		k.vstart = i
		ve, err := jscanValue(s, i)
		if err != nil {
			return c, err
		}
		k.vend = ve
		c.kids = append(c.kids, k)
		i = jskipSpace(s, ve)
		switch {
		case i < len(s) && s[i] == ',':
			i = jskipSpace(s, i+1)
		case i < len(s) && s[i] == end:
			c.close = i
			return c, nil
		default:
			return c, errScan
		}
	}
}

// find returns the index of el in c (the last member with that key, as JSON readers take), or -1.
func (c jcontainer) find(el any) (int, error) {
	switch v := el.(type) {
	case string:
		if c.list {
			return 0, fmt.Errorf("%q: the value holding it is a list, not an object", v)
		}
		for i := len(c.kids) - 1; i >= 0; i-- {
			if c.kids[i].key == v {
				return i, nil
			}
		}
		return -1, nil
	case int:
		if !c.list {
			return 0, fmt.Errorf("[%d]: the value holding it is an object, not a list", v)
		}
		if v < 0 || v >= len(c.kids) {
			return -1, nil
		}
		return v, nil
	}
	return 0, fmt.Errorf("bad path element %v", el)
}

// rootContainer is the top-level object.
func (d *jsonDoc) rootContainer() (jcontainer, error) {
	i := jskipSpace(d.text, 0)
	if i >= len(d.text) || d.text[i] != '{' {
		return jcontainer{}, errScan
	}
	return jscanContainer(d.text, i)
}

// locate follows p as far as it exists. It returns the deepest container reached, the number n of path elements
// that led to it, and the index in it of p[n] (-1 when missing). When n < len(p)-1 and the index is not -1, p[n]
// holds a value that is not a container.
func (d *jsonDoc) locate(p jpath) (c jcontainer, n, idx int, err error) {
	if c, err = d.rootContainer(); err != nil {
		return c, 0, -1, err
	}
	for n = 0; n < len(p); n++ {
		if idx, err = c.find(p[n]); err != nil {
			return c, n, -1, err
		}
		if idx < 0 || n == len(p)-1 {
			return c, n, idx, nil
		}
		k := c.kids[idx]
		if b := d.text[k.vstart]; b != '{' && b != '[' {
			return c, n, idx, nil
		}
		if c, err = jscanContainer(d.text, k.vstart); err != nil {
			return c, n, -1, err
		}
	}
	return c, n, -1, errors.New("empty JSON path")
}

// set sets the value at p, creating the objects on the way (a missing or null one). Repeats of the key are
// dropped. Only the bytes of the changed value (or the new member) change.
func (d *jsonDoc) set(p jpath, v json.RawMessage) error {
	for {
		c, n, idx, err := d.locate(p)
		if err != nil {
			return err
		}
		if idx >= 0 && n == len(p)-1 {
			if dup := c.repeatOf(idx); dup >= 0 {
				if err := d.deleteKid(c, dup); err != nil {
					return err
				}
				continue
			}
			return d.replaceValue(c.kids[idx], v)
		}
		rest, err := nested(p[n+1:], v)
		if err != nil {
			return err
		}
		if idx >= 0 { // a scalar where an object should be
			k := c.kids[idx]
			if string(d.text[k.vstart:k.vend]) != "null" {
				return fmt.Errorf("%v is not a JSON object", p[n])
			}
			return d.replaceValue(k, rest)
		}
		key, ok := p[n].(string)
		if !ok {
			return fmt.Errorf("list index %v is out of range", p[n])
		}
		return d.insertKid(c, key, rest)
	}
}

// del removes the member or element at p (every repeat of the key); a missing one is no error.
func (d *jsonDoc) del(p jpath) error {
	for {
		c, n, idx, err := d.locate(p)
		if err != nil || idx < 0 || n < len(p)-1 {
			return err
		}
		if err := d.deleteKid(c, idx); err != nil {
			return err
		}
		if _, isIndex := p[n].(int); isIndex {
			return nil
		}
	}
}

// appendTo adds v at the end of the list at p, creating the list when it is missing or null.
func (d *jsonDoc) appendTo(p jpath, v json.RawMessage) error {
	c, n, idx, err := d.locate(p)
	if err != nil {
		return err
	}
	if idx < 0 || n < len(p)-1 || string(d.text[c.kids[idx].vstart:c.kids[idx].vend]) == "null" {
		var list bytes.Buffer
		list.WriteByte('[')
		list.Write(v)
		list.WriteByte(']')
		return d.set(p, list.Bytes())
	}
	k := c.kids[idx]
	if d.text[k.vstart] != '[' {
		return fmt.Errorf("%v is not a list", p[n])
	}
	l, err := jscanContainer(d.text, k.vstart)
	if err != nil {
		return err
	}
	return d.insertKid(l, "", v)
}

// isEmptyAt reports whether the value at p is an empty object or list; a missing value is not.
func (d *jsonDoc) isEmptyAt(p jpath) bool {
	c, n, idx, err := d.locate(p)
	if err != nil || idx < 0 || n < len(p)-1 {
		return false
	}
	k := c.kids[idx]
	if b := d.text[k.vstart]; b != '{' && b != '[' {
		return false
	}
	inner, err := jscanContainer(d.text, k.vstart)
	return err == nil && len(inner.kids) == 0
}

// repeatOf returns the index of another member with the key of member idx, or -1.
func (c jcontainer) repeatOf(idx int) int {
	if c.list {
		return -1
	}
	for i, k := range c.kids {
		if i != idx && k.key == c.kids[idx].key {
			return i
		}
	}
	return -1
}

// nested wraps v in objects for the keys of p: nested({"a","b"}, v) is {"a":{"b":v}}.
func nested(p jpath, v json.RawMessage) (json.RawMessage, error) {
	for i := len(p) - 1; i >= 0; i-- {
		key, ok := p[i].(string)
		if !ok {
			return nil, fmt.Errorf("list index %v does not exist", p[i])
		}
		k, err := marshalNoEscape(key)
		if err != nil {
			return nil, err
		}
		v = json.RawMessage("{" + string(k) + ":" + string(v) + "}")
	}
	return v, nil
}

// splice replaces text[from:to] with repl and parses the result again.
func (d *jsonDoc) splice(from, to int, repl string) error {
	text := make([]byte, 0, len(d.text)-(to-from)+len(repl))
	text = append(append(append(text, d.text[:from]...), repl...), d.text[to:]...)
	root, err := parseObject(text)
	if err != nil {
		return fmt.Errorf("editing the JSON text went wrong (nothing was written): %w", err)
	}
	d.text, d.root = text, root
	return nil
}

// replaceValue replaces the value of k with v, laid out like the line k starts on.
func (d *jsonDoc) replaceValue(k jspan, v json.RawMessage) error {
	ws := d.spaceBefore(k.start)
	val, err := d.render(v, strings.Contains(ws, "\n"), lineIndent(d.text, k.start), d.commaSpace())
	if err != nil {
		return err
	}
	return d.splice(k.vstart, k.vend, val)
}

// insertKid adds a member key: v (or, in a list, the element v) at the end of c, laid out like its last member:
// on its own line with the same indentation, or on the same line for a one-line container.
func (d *jsonDoc) insertKid(c jcontainer, key string, v json.RawMessage) error {
	multi, ind, sep := true, lineIndent(d.text, c.open)+d.indent, ""
	if len(c.kids) > 0 {
		ws := d.spaceBefore(c.kids[len(c.kids)-1].start)
		if i := strings.LastIndexByte(ws, '\n'); i >= 0 {
			ind = ws[i+1:]
		} else {
			multi, sep = false, ws
			if len(c.kids) == 1 { // the space after the brace says nothing about the space after commas
				sep = d.commaSpace()
			}
		}
	} else {
		multi = d.pretty()
	}
	member := ""
	if !c.list {
		k, err := marshalNoEscape(key)
		if err != nil {
			return err
		}
		member = string(k) + d.colon()
	}
	val, err := d.render(v, multi, ind, d.commaSpace())
	if err != nil {
		return err
	}
	member += val
	switch {
	case len(c.kids) == 0 && multi:
		return d.splice(c.open+1, c.close, d.nl()+ind+member+d.nl()+lineIndent(d.text, c.open))
	case len(c.kids) == 0:
		return d.splice(c.open+1, c.close, member)
	case multi:
		end := c.kids[len(c.kids)-1].vend
		return d.splice(end, end, ","+d.nl()+ind+member)
	default:
		end := c.kids[len(c.kids)-1].vend
		return d.splice(end, end, ","+sep+member)
	}
}

// deleteKid removes member or element idx of c with its comma. The space before the following member stays, so
// the rest of the container keeps its layout.
func (d *jsonDoc) deleteKid(c jcontainer, idx int) error {
	switch {
	case len(c.kids) == 1:
		return d.splice(c.open+1, c.close, "")
	case idx == 0:
		return d.splice(c.kids[0].start, c.kids[1].start, "")
	default:
		return d.splice(c.kids[idx-1].vend, c.kids[idx].vend, "")
	}
}

// render lays out v: on one line with the file's colon and comma spacing, or indented as a block whose first line
// continues the current one and whose other lines start with ind, in the file's indentation unit and line ends.
func (d *jsonDoc) render(v json.RawMessage, multi bool, ind, comma string) (string, error) {
	var b bytes.Buffer
	if !multi {
		if err := json.Compact(&b, v); err != nil {
			return "", err
		}
		return spaceOut(b.Bytes(), d.colon(), comma), nil
	}
	if err := json.Indent(&b, v, ind, d.indent); err != nil {
		return "", err
	}
	out := b.String()
	if d.crlf {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	return out, nil
}

// spaceOut puts colon after every key and comma after every comma of compact JSON (outside strings).
func spaceOut(compact []byte, colon, comma string) string {
	var b strings.Builder
	inStr, esc := false, false
	for _, c := range compact {
		switch {
		case inStr:
			b.WriteByte(c)
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
		case c == '"':
			inStr = true
			b.WriteByte(c)
		case c == ':':
			b.WriteString(colon)
		case c == ',':
			b.WriteByte(',')
			b.WriteString(comma)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// commaSpace is the space a one-line value gets after its commas: one space when the file's colon has one.
func (d *jsonDoc) commaSpace() string {
	if strings.HasSuffix(d.colon(), " ") {
		return " "
	}
	return ""
}

// spaceBefore is the white space right before text[i], without carriage returns.
func (d *jsonDoc) spaceBefore(i int) string {
	j := i
	for j > 0 && isJSONSpace(d.text[j-1]) {
		j--
	}
	return strings.ReplaceAll(string(d.text[j:i]), "\r", "")
}

// lineIndent is the indentation of the line holding text[i].
func lineIndent(text []byte, i int) string {
	start := bytes.LastIndexByte(text[:i], '\n') + 1
	end := start
	for end < len(text) && (text[end] == ' ' || text[end] == '\t') {
		end++
	}
	return string(text[start:end])
}

// pretty reports whether new members of an empty container go on their own lines: yes, unless the file is a
// non-empty one-line document.
func (d *jsonDoc) pretty() bool {
	if bytes.ContainsRune(d.text, '\n') {
		return true
	}
	root, err := d.rootContainer()
	return err != nil || len(root.kids) == 0
}

// colon is what the file puts between a key and its value (": " unless its first member says otherwise).
func (d *jsonDoc) colon() string {
	root, err := d.rootContainer()
	if err != nil || len(root.kids) == 0 {
		return ": "
	}
	c := string(d.text[root.kids[0].keyEnd:root.kids[0].vstart])
	if strings.ContainsAny(c, "\r\n") || strings.Count(c, ":") != 1 {
		return ": "
	}
	return c
}

// onlyEmpty reports whether the document holds nothing but, at most, an empty object or list under one of keys:
// what is left of a file setup created once its entry is gone.
func (d *jsonDoc) onlyEmpty(keys ...string) bool {
	switch len(d.root.members) {
	case 0:
		return true
	case 1:
		k := d.root.members[0].key
		for _, want := range keys {
			if k == want {
				return d.isEmptyAt(jpath{k})
			}
		}
	}
	return false
}
