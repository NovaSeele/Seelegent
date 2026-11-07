package migrate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// maxReverts bounds the rounds of putting back defaults for invalid settings.
const maxReverts = 40

// importSettings imports settings.json into config.json and returns the settings now in force together with the
// old "providers" object.
func (m *migrator) importSettings() (inForce config.Settings, providers json.RawMessage) {
	cur, err := config.Load(m.o.Paths.Config)
	if err != nil {
		m.add(KindSettings, "", Error, "config.json cannot be read, so the old settings were not imported: %v", err)
		return config.Defaults(), m.oldProviders()
	}
	data, err := fsutil.ReadFile(m.old("settings.json"))
	if errors.Is(err, fs.ErrNotExist) {
		m.add(KindSettings, "", Skipped, "no settings.json in the old folder")
		return cur.Settings, nil
	}
	if err != nil {
		m.add(KindSettings, "", Error, "settings.json: %v", err)
		return cur.Settings, nil
	}
	top, err := decodeObject(data)
	if err != nil {
		m.add(KindSettings, "", Error, "settings.json is not a JSON object: %v", err)
		return cur.Settings, nil
	}
	providers, _ = json.Marshal(top["providers"])
	mirrorLegacy(top)
	s, notes := parseLenient(top)
	notes = append(notes, closeEmptyPolicies(top, &s)...)
	detail := "imported settings.json"
	if len(notes) > 0 {
		detail += "; " + strings.Join(notes, "; ")
	}
	switch {
	case cur.Found && reflect.DeepEqual(cur.Settings, s):
		m.add(KindSettings, "", Unchanged, "config.json already holds these settings")
		return cur.Settings, providers
	case cur.Found && !reflect.DeepEqual(cur.Settings, config.Defaults()):
		m.add(KindSettings, "", Kept, "config.json already has settings of this version that differ from the "+
			"defaults; it was left as it is")
		return cur.Settings, providers
	}
	if !m.o.DryRun {
		c := &config.Config{Settings: s}
		if err := c.Save(m.o.Paths.Config); err != nil {
			m.add(KindSettings, "", Error, "%v", err)
			return cur.Settings, providers
		}
	}
	m.add(KindSettings, "", Imported, "%s", detail)
	return s, providers
}

// oldProviders reads only the providers of settings.json (when config.json is unreadable).
func (m *migrator) oldProviders() json.RawMessage {
	data, err := fsutil.ReadFile(m.old("settings.json"))
	if err != nil {
		return nil
	}
	top, err := decodeObject(data)
	if err != nil {
		return nil
	}
	raw, _ := json.Marshal(top["providers"])
	return raw
}

func decodeObject(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	dec.UseNumber()
	var top map[string]any
	if err := dec.Decode(&top); err != nil {
		return nil, err
	}
	if top == nil {
		return nil, errors.New("null")
	}
	return top, nil
}

// mirrorLegacy applies the old version's precedence: the top-level "tiers" and "allowedModels" (which it always
// wrote) are what the agy backend used.
func mirrorLegacy(top map[string]any) {
	tiers, hasTiers := top["tiers"].(map[string]any)
	allowed, hasAllowed := top["allowedModels"]
	if !hasTiers && !hasAllowed {
		return
	}
	backends, _ := top["backends"].(map[string]any)
	if backends == nil {
		backends = map[string]any{}
		top["backends"] = backends
	}
	agy, _ := backends["agy"].(map[string]any)
	if agy == nil {
		agy = map[string]any{}
		backends["agy"] = agy
	}
	if hasTiers {
		agy["tiers"] = tiers
	}
	if hasAllowed {
		agy["allowedModels"] = allowed
	}
}

// parseLenient parses the settings, putting back the default of every setting this version refuses, and says
// which ones.
func parseLenient(top map[string]any) (s config.Settings, notes []string) {
	for range maxReverts {
		data, err := json.Marshal(top)
		if err != nil {
			return config.Defaults(), []string{"the settings could not be read; the defaults stay"}
		}
		c, err := config.Parse(data)
		if err == nil {
			return c.Settings, notes
		}
		var fields []config.FieldError
		var verr *config.ValidationError
		var typ *json.UnmarshalTypeError
		switch {
		case errors.As(err, &verr):
			fields = verr.Fields
		case errors.As(err, &typ) && typ.Field != "":
			fields = []config.FieldError{{Field: typ.Field, Message: "has the wrong type"}}
		default:
			return config.Defaults(), append(notes, fmt.Sprintf("the settings could not be read (%v); the "+
				"defaults stay", err))
		}
		removed := false
		for _, f := range fields {
			if path := revert(top, f.Field); path != "" {
				removed = true
				notes = append(notes, fmt.Sprintf("kept the default of %s (%s: %s)", path, f.Field, f.Message))
			}
		}
		if !removed {
			return config.Defaults(), append(notes, fmt.Sprintf("the settings could not be fixed (%v); the "+
				"defaults stay", err))
		}
	}
	return config.Defaults(), append(notes, "too many invalid settings; the defaults stay")
}

// revert deletes the setting at field (a JSON path such as "backends.agy.tiers.fast" or "linkIntoProfiles[2]"),
// or the nearest enclosing setting that is still there, and returns the path it deleted ("" when none).
func revert(top map[string]any, field string) string {
	parts := strings.Split(field, ".")
	for i, p := range parts {
		if j := strings.IndexByte(p, '['); j >= 0 {
			parts = parts[:i+1]
			parts[i] = p[:j]
			break
		}
	}
	for n := len(parts); n > 0; n-- {
		obj := top
		ok := true
		for _, p := range parts[:n-1] {
			if obj, ok = obj[p].(map[string]any); !ok {
				break
			}
		}
		if !ok {
			continue
		}
		if _, there := obj[parts[n-1]]; there {
			delete(obj, parts[n-1])
			return strings.Join(parts[:n], ".")
		}
	}
	return ""
}

// closeEmptyPolicies keeps the old meaning of an empty allowedModels for codex and claude, which allowed no model
// at all there (in this version empty allows any model): such a backend now allows only its tier models.
func closeEmptyPolicies(top map[string]any, s *config.Settings) []string {
	var notes []string
	for _, b := range []struct {
		id string
		be *config.Backend
	}{{config.BackendCodex, &s.Backends.Codex.Backend}, {config.BackendClaude, &s.Backends.Claude}} {
		backends, _ := top["backends"].(map[string]any)
		own, _ := backends[b.id].(map[string]any)
		if v, set := own["allowedModels"]; set && v != "" {
			continue
		}
		if b.be.AllowedModels != "" {
			continue
		}
		b.be.AllowedModels = tierPattern(b.be.Tiers)
		notes = append(notes, fmt.Sprintf("%s allowed no model in the old version (empty allowedModels); it now "+
			"allows only its tier models (%s)", b.id, b.be.AllowedModels))
	}
	return notes
}

// tierPattern is the pattern that allows exactly the tier models, or no model when no tier is set.
func tierPattern(t config.TierModels) string {
	var models []string
	for _, m := range []string{t.Fast, t.Standard, t.Strong, t.Max} {
		if m = strings.TrimSpace(m); m != "" {
			models = append(models, m)
		}
	}
	if p := account.ModelsPattern(models); p != "" {
		return p
	}
	return "^$"
}
