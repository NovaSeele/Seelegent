package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
)

// MergePatch applies a JSON merge patch (RFC 7396) to s and returns the result, validated. A null value resets
// that setting to its default; an object merges into the setting's object; anything else replaces the value
// (arrays included). The patch is all-or-nothing: unknown keys, values of the wrong type, a changed port and every
// out-of-range value are reported together as a *ValidationError (matching ErrInvalid), and s is left as it was.
// A body that is not a JSON object (null, an array, broken JSON) matches ErrBadPatch.
func MergePatch(s Settings, patch []byte) (Settings, error) {
	if len(bytes.TrimSpace(patch)) == 0 {
		patch = []byte("{}")
	}
	var p map[string]any
	if err := decodeNumbers(patch, &p); err != nil {
		return s, fmt.Errorf("%w (%w)", ErrBadPatch, err)
	}
	if p == nil {
		return s, ErrBadPatch
	}
	cur, err := toTree(s)
	if err != nil {
		return s, err
	}
	def, err := toTree(Defaults())
	if err != nil {
		return s, err
	}
	// An auto setting left on auto is no key in the tree (omitempty); it is still a known one.
	for _, k := range AutoSettingKeys() {
		if _, ok := cur[k]; !ok {
			cur[k] = nil
		}
		if _, ok := def[k]; !ok {
			def[k] = nil
		}
	}
	v := &validator{}
	applyPatch(v, cur, def, p, "")
	// Unknown keys were left out of cur, so the checks below still run and every problem is reported at once.
	raw, err := json.Marshal(cur)
	if err != nil {
		return s, err
	}
	out := Settings{}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	typeField := ""
	if err := dec.Decode(&out); err != nil {
		var te *json.UnmarshalTypeError
		if !errors.As(err, &te) {
			return s, fmt.Errorf("%w: %w", ErrParse, err)
		}
		// encoding/json reports the first wrong type only.
		typeField = te.Field
		v.add(te.Field, "must be %s", jsonKind(te.Type.Kind().String()))
	}
	if out.LinkIntoProfiles == nil {
		out.LinkIntoProfiles = []string{}
	}
	if out.GithubEnterpriseHosts == nil {
		out.GithubEnterpriseHosts = []string{}
	}
	if out.Port != s.Port && typeField != "port" {
		v.add("port", "cannot be changed here; edit config.json or set SEELEGENT_PORT, then restart the daemon")
	}
	// After a type error the decoded settings are incomplete; range checks would repeat that field.
	if err := out.Validate(); err != nil && typeField == "" {
		var ve *ValidationError
		if !errors.As(err, &ve) {
			return s, err
		}
		v.errs = append(v.errs, ve.Fields...)
	}
	if len(v.errs) > 0 {
		return s, v.err()
	}
	return out, nil
}

// applyPatch merges patch into cur; def holds the defaults at the same level (nil below a key without default).
func applyPatch(v *validator, cur, def, patch map[string]any, prefix string) {
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		path := prefix + k
		old, known := cur[k]
		if !known {
			v.add(path, "is not a known setting")
			continue
		}
		val := patch[k]
		switch pv := val.(type) {
		case nil:
			cur[k] = def[k]
		case map[string]any:
			oldObj, ok := old.(map[string]any)
			if !ok {
				cur[k] = pv // a type error, reported when decoding
				continue
			}
			defObj, _ := def[k].(map[string]any)
			applyPatch(v, oldObj, defObj, pv, path+".")
		default:
			cur[k] = pv
		}
	}
}

// toTree turns settings into a generic JSON tree.
func toTree(s Settings) (map[string]any, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := decodeNumbers(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// decodeNumbers decodes one JSON value, keeping numbers as json.Number so integers stay exact.
func decodeNumbers(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("unexpected data after the JSON value")
	}
	return nil
}
