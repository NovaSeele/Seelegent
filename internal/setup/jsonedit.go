package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// The JSON editor keeps a host's config file as the user wrote it as far as JSON allows: the order of keys, every
// value it does not touch (numbers keep their exact spelling), a UTF-8 byte order mark, CRLF line ends, the
// indentation unit and the final newline. Only the indentation of lines is normalised, because the file is
// re-indented as a whole.

// errJSONC says a config file is valid only once comments or trailing commas are removed: setup cannot edit it
// without losing them, so the user gets a snippet to paste instead.
var errJSONC = errors.New("the file has comments or trailing commas")

// jmember is one key of a JSON object with its raw value.
type jmember struct {
	key string
	val json.RawMessage
}

// jobject is a JSON object that remembers the order of its keys.
type jobject struct {
	members []jmember
}

// parseObject reads a JSON object, keeping the order of its keys and the raw text of their values.
func parseObject(data []byte) (*jobject, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not a JSON object")
	}
	o := &jobject{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, errors.New("object key is not a string")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		o.members = append(o.members, jmember{key: key, val: raw})
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected data after the object")
		}
		return nil, err
	}
	return o, nil
}

// get returns the value of key (the last one when the key repeats, as JSON readers do).
func (o *jobject) get(key string) (json.RawMessage, bool) {
	for i := len(o.members) - 1; i >= 0; i-- {
		if o.members[i].key == key {
			return o.members[i].val, true
		}
	}
	return nil, false
}

// set replaces the value of key in place (dropping repeats of the key) or appends the key.
func (o *jobject) set(key string, val json.RawMessage) {
	found := false
	out := o.members[:0]
	for _, m := range o.members {
		if m.key == key {
			if found {
				continue
			}
			m.val, found = val, true
		}
		out = append(out, m)
	}
	o.members = out
	if !found {
		o.members = append(o.members, jmember{key: key, val: val})
	}
}

// del removes key.
func (o *jobject) del(key string) {
	o.members = slices.DeleteFunc(o.members, func(m jmember) bool { return m.key == key })
}

// compact returns the object as compact JSON.
func (o *jobject) compact() (json.RawMessage, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range o.members {
		if i > 0 {
			b.WriteByte(',')
		}
		k, err := marshalNoEscape(m.key)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		if err := json.Compact(&b, m.val); err != nil {
			return nil, fmt.Errorf("value of %q: %w", m.key, err)
		}
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// marshalNoEscape is json.Marshal without HTML escaping (paths and commands stay readable).
func marshalNoEscape(v any) (json.RawMessage, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// childObject returns the object under key, a new empty one when the key is missing or null, or an error when the
// value is something else.
func (o *jobject) childObject(key string) (*jobject, error) {
	raw, ok := o.get(key)
	if !ok || string(bytes.TrimSpace(raw)) == "null" {
		return &jobject{}, nil
	}
	c, err := parseObject(raw)
	if err != nil {
		return nil, fmt.Errorf("%q is not a JSON object", key)
	}
	return c, nil
}

// jsonDoc is a host config file holding a JSON object, with what is needed to write it back in its own style.
type jsonDoc struct {
	root       *jobject
	bom        bool
	crlf       bool
	indent     string
	trailingNL bool
	existed    bool
}

const utf8BOM = "\xef\xbb\xbf"

// loadJSONDoc parses a config file's content. Empty content gives an empty object. A file that parses only without
// comments or trailing commas fails with errJSONC.
func loadJSONDoc(data []byte, existed bool) (*jsonDoc, error) {
	d := &jsonDoc{indent: "  ", trailingNL: true, existed: existed}
	text := string(data)
	if strings.HasPrefix(text, utf8BOM) {
		d.bom = true
		text = text[len(utf8BOM):]
	}
	if strings.TrimSpace(text) == "" {
		d.root = &jobject{}
		return d, nil
	}
	d.crlf = strings.Contains(text, "\r\n")
	d.trailingNL = strings.HasSuffix(text, "\n")
	d.indent = detectIndent(text)
	root, err := parseObject([]byte(text))
	if err != nil {
		if _, err2 := parseObject([]byte(stripJSONC(text))); err2 == nil {
			return nil, errJSONC
		}
		return nil, fmt.Errorf("malformed JSON: %w", err)
	}
	d.root = root
	return d, nil
}

// bytes renders the document in the file's style.
func (d *jsonDoc) bytes() ([]byte, error) {
	compact, err := d.root.compact()
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := json.Indent(&b, compact, "", d.indent); err != nil {
		return nil, err
	}
	out := b.String()
	if d.trailingNL {
		out += "\n"
	}
	if d.crlf {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	if d.bom {
		out = utf8BOM + out
	}
	return []byte(out), nil
}

// detectIndent returns the indentation unit of the first indented line, or two spaces.
func detectIndent(text string) string {
	for line := range strings.Lines(text) {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == line || strings.TrimSpace(trimmed) == "" {
			continue
		}
		ws := line[:len(line)-len(trimmed)]
		if ws[0] == '\t' {
			return "\t"
		}
		return ws
	}
	return "  "
}

// stripJSONC removes // and /* */ comments outside strings and commas right before a closing bracket or brace.
func stripJSONC(s string) string {
	var b strings.Builder
	inStr, esc := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
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
		case c == '/' && i+1 < len(s) && s[i+1] == '/':
			for i < len(s) && s[i] != '\n' {
				i++
			}
			if i < len(s) {
				b.WriteByte('\n')
			}
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return b.String()
			}
			i += end + 3
		default:
			b.WriteByte(c)
		}
	}
	return removeTrailingCommas(b.String())
}

// removeTrailingCommas drops a comma followed (after white space) by ] or }, outside strings.
func removeTrailingCommas(s string) string {
	var b strings.Builder
	inStr, esc := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			b.WriteByte(c)
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
		}
		if c == ',' {
			j := i + 1
			for j < len(s) && strings.IndexByte(" \t\r\n", s[j]) >= 0 {
				j++
			}
			if j < len(s) && (s[j] == ']' || s[j] == '}') {
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}
