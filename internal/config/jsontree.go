package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// member is one key of a JSON object with its raw value.
type member struct {
	key   string
	value json.RawMessage
}

// object is a JSON object that keeps its keys in document order and its values as raw JSON, so that keys this
// version does not know can be written back exactly as they were read.
type object struct {
	members []member
}

// errNotObject reports a document whose top level is not a JSON object.
var errNotObject = errors.New("the top level must be a JSON object")

// parseObject reads a JSON object. A later duplicate key replaces the earlier value, as encoding/json does.
func parseObject(data []byte) (*object, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if tok != json.Delim('{') {
		return nil, errNotObject
	}
	o := &object{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("unexpected object key %v", tok)
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		o.set(key, value)
	}
	if _, err := dec.Token(); err != nil { // closing brace
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected data after the top-level object")
	}
	return o, nil
}

// get returns the raw value of key.
func (o *object) get(key string) (json.RawMessage, bool) {
	for _, m := range o.members {
		if m.key == key {
			return m.value, true
		}
	}
	return nil, false
}

// set replaces the value of key, or appends key if it is new.
func (o *object) set(key string, value json.RawMessage) {
	for i := range o.members {
		if o.members[i].key == key {
			o.members[i].value = value
			return
		}
	}
	o.members = append(o.members, member{key: key, value: value})
}

// marshal encodes the object compactly, keys in order.
func (o *object) marshal() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, m := range o.members {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(m.key)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(m.value)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// isObject reports whether raw holds a JSON object.
func isObject(raw json.RawMessage) bool {
	t := bytes.TrimLeft(raw, " \t\r\n")
	return len(t) > 0 && t[0] == '{'
}

// overlay returns known with the keys of orig that known does not have appended after its own keys, recursing
// into values that are objects on both sides. It also returns the JSON paths of those extra keys. A key of orig
// that differs from a key of known only in letter case is an error: encoding/json would have read it as the known
// key, so keeping it would write the same setting twice.
func overlay(known, orig *object, prefix string) (*object, []string, error) {
	out := &object{}
	var extra []string
	for _, m := range known.members {
		value := m.value
		if o, ok := orig.get(m.key); ok && isObject(value) && isObject(o) {
			kObj, err := parseObject(value)
			if err != nil {
				return nil, nil, err
			}
			oObj, err := parseObject(o)
			if err != nil {
				return nil, nil, err
			}
			merged, sub, err := overlay(kObj, oObj, prefix+m.key+".")
			if err != nil {
				return nil, nil, err
			}
			if value, err = merged.marshal(); err != nil {
				return nil, nil, err
			}
			extra = append(extra, sub...)
		}
		out.members = append(out.members, member{key: m.key, value: value})
	}
	for _, m := range orig.members {
		if _, ok := known.get(m.key); ok {
			continue
		}
		for _, k := range known.members {
			if strings.EqualFold(k.key, m.key) {
				return nil, nil, &ValidationError{Fields: []FieldError{{
					Field:   prefix + m.key,
					Message: fmt.Sprintf("unknown key differs from %q only in letter case; rename it to %q", prefix+k.key, k.key),
				}}}
			}
		}
		out.members = append(out.members, m)
		extra = append(extra, prefix+m.key)
	}
	return out, extra, nil
}
