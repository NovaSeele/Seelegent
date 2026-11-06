package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// oldSettings is a settings.json as the old Node daemon wrote it, plus a newer version's key.
const oldSettings = `{
  "port": 47821,
  "tiers": {"fast": "gemini-3.8-flash-low"},
  "allowedModels": "^gemini-3\\.8-",
  "defaultTier": "strong",
  "backends": {
    "agy": {"enabled": true, "allowedModels": "^gemini-3\\.8-", "tiers": {"fast": "gemini-3.8-flash-low", "standard": "gemini-3.8-flash-medium", "strong": "gemini-3.8-flash-high", "max": "gemini-3.8-flash-high", "ultra": "x"}, "futureFlag": 1},
    "opencode": {"enabled": false}
  },
  "providers": {"deepseek": {"harness": "claude", "baseUrl": "https://api.deepseek.com/anthropic"}},
  "updates": "notify",
  "stallMinutes": 12,
  "linkIntoProfiles": [".ssh"]
}
`

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMissingGivesDefaultsAndWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Found {
		t.Error("Found = true for a missing file")
	}
	if !reflect.DeepEqual(c.Settings, Defaults()) {
		t.Error("settings of a missing file must be the defaults")
	}
	if len(c.Unknown()) != 0 {
		t.Errorf("Unknown = %v", c.Unknown())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Load must not create the file")
	}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	if !c.Found {
		t.Error("Found must be true after Save")
	}
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again.Settings, Defaults()) {
		t.Error("saved defaults did not load back as the defaults")
	}
}

func TestLoadOldSettings(t *testing.T) {
	c, err := Load(writeFile(t, oldSettings))
	if err != nil {
		t.Fatal(err)
	}
	s := c.Settings
	if s.DefaultTier != TierStrong || s.StallMinutes != 12 || !reflect.DeepEqual(s.LinkIntoProfiles, []string{".ssh"}) {
		t.Errorf("file values not applied: %+v", s)
	}
	if s.TaskTimeoutMinutes != 45 || s.Backends.Codex.Sandbox != SandboxWorkspaceWrite {
		t.Errorf("missing keys did not take their defaults: %+v", s)
	}
	want := []string{
		"backends.agy.tiers.ultra", "backends.agy.futureFlag", "backends.opencode",
		"tiers", "allowedModels", "providers", "updates",
	}
	if got := c.Unknown(); !reflect.DeepEqual(got, want) {
		t.Errorf("Unknown = %v\nwant      %v", got, want)
	}
}

func TestSaveKeepsUnknownKeys(t *testing.T) {
	path := writeFile(t, oldSettings)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	c.Settings.Port = 47900
	c.Settings.Backends.Agy.Tiers.Fast = "gemini-3.8-flash-medium"
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("saved file is not JSON: %v\n%s", err, data)
	}
	if doc["port"] != float64(47900) {
		t.Errorf("port = %v", doc["port"])
	}
	agy := doc["backends"].(map[string]any)["agy"].(map[string]any)
	if agy["futureFlag"] != float64(1) || agy["tiers"].(map[string]any)["ultra"] != "x" {
		t.Errorf("nested unknown keys lost: %v", agy)
	}
	if agy["tiers"].(map[string]any)["fast"] != "gemini-3.8-flash-medium" {
		t.Errorf("known nested value not updated: %v", agy["tiers"])
	}
	for _, k := range []string{"providers", "updates", "tiers", "allowedModels"} {
		if _, ok := doc[k]; !ok {
			t.Errorf("unknown top-level key %q lost", k)
		}
	}
	if _, ok := doc["backends"].(map[string]any)["opencode"]; !ok {
		t.Error("backends.opencode lost")
	}
	// Known settings come first, in struct order, then the unknown keys.
	if !bytes.HasPrefix(data, []byte("{\n  \"port\": 47900,\n  \"defaultTier\"")) || !bytes.HasSuffix(data, []byte("}\n")) {
		t.Errorf("unexpected layout:\n%s", data)
	}
	if bytes.Index(data, []byte(`"automationsEnabled"`)) > bytes.Index(data, []byte(`"providers"`)) {
		t.Error("unknown keys must follow the known settings")
	}
	// Saving again without changes is stable.
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := again.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(second, data) {
		t.Errorf("second save differs:\n%s\nvs\n%s", second, data)
	}
}

// Bug 6 of the old version: a parse error returned the defaults and the next save wiped the real file.
func TestLoadBrokenFileIsAnErrorAndUntouched(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr error
		wantMsg string
	}{
		{"truncated", "{\n  \"port\": 47821,\n  \"gates\": tr", ErrParse, "line 3"},
		{"trailing comma", "{\"port\": 1,}", ErrParse, "line 1, column 12"},
		{"empty", "", ErrParse, "empty"},
		{"whitespace", " \n", ErrParse, "empty"},
		{"null", "null", ErrParse, "object"},
		{"array", "[1]", ErrParse, "an object"},
		{"string", `"x"`, ErrParse, "an object"},
		{"wrong type", "{\n\"port\": \"47821\"}", ErrParse, "port must be a number, not a JSON string"},
		{"wrong nested type", `{"backends": {"agy": {"enabled": "yes"}}}`, ErrParse, "backends.agy.enabled must be true or false"},
		{"two documents", `{"port": 1}{"port": 2}`, ErrParse, ""},
		{"out of range", `{"port": 0}`, ErrInvalid, "port: must be between 1 and 65535"},
		{"case twin", `{"Port": 1}`, ErrInvalid, `differs from "port" only in letter case`},
		{"nested case twin", `{"backends": {"agy": {"Enabled": false}}}`, ErrInvalid, "backends.agy.Enabled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeFile(t, tt.content)
			c, err := Load(path)
			if c != nil || !errors.Is(err, tt.wantErr) {
				t.Fatalf("Load = %v, %v; want nil and %v", c, err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) || !strings.Contains(err.Error(), path) {
				t.Errorf("error %q should name the file and contain %q", err, tt.wantMsg)
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil || string(data) != tt.content {
				t.Fatalf("file changed after a failed Load: %q, %v", data, rerr)
			}
			if _, uerr := Update(t.Context(), path, func(*Settings) error { return nil }); !errors.Is(uerr, tt.wantErr) {
				t.Fatalf("Update on a broken file: err = %v, want %v", uerr, tt.wantErr)
			}
			if data, _ := os.ReadFile(path); string(data) != tt.content {
				t.Fatal("Update replaced a broken file")
			}
		})
	}
}

func TestLoadUnreadable(t *testing.T) {
	dir := t.TempDir() // a directory cannot be read as a file
	_, err := Load(dir)
	if err == nil || errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrParse) {
		t.Fatalf("err = %v, want a read error", err)
	}
}

func TestNullKeepsDefault(t *testing.T) {
	c, err := Parse([]byte(`{"linkIntoProfiles": null, "port": null}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Settings.Port != DefaultPort || len(c.Settings.LinkIntoProfiles) == 0 {
		t.Fatalf("null must keep the default: %+v", c.Settings)
	}
	c, err = Parse([]byte(`{"linkIntoProfiles": []}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Settings.LinkIntoProfiles == nil || len(c.Settings.LinkIntoProfiles) != 0 {
		t.Fatalf("[] must link nothing: %#v", c.Settings.LinkIntoProfiles)
	}
}

func TestSaveInvalidWritesNothing(t *testing.T) {
	path := writeFile(t, `{"port": 47821}`)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	c.Settings.MaxConcurrentPerAccount = 0
	if err := c.Save(path); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Save: err = %v, want ErrInvalid", err)
	}
	if data, _ := os.ReadFile(path); string(data) != `{"port": 47821}` {
		t.Fatalf("file changed: %s", data)
	}
}

func TestSaveNilLinksAndMissingDir(t *testing.T) {
	c := &Config{Settings: Defaults()}
	c.Settings.LinkIntoProfiles = nil
	data, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"linkIntoProfiles": []`)) {
		t.Errorf("nil list must be written as []:\n%s", data)
	}
	if err := c.Save(filepath.Join(t.TempDir(), "missing", "config.json")); err == nil {
		t.Fatal("Save into a missing directory must fail")
	}
}

func TestUnknownIsACopy(t *testing.T) {
	c, err := Parse([]byte(`{"extra": 1}`))
	if err != nil {
		t.Fatal(err)
	}
	u := c.Unknown()
	u[0] = "changed"
	if c.Unknown()[0] != "extra" {
		t.Fatal("Unknown must return a copy")
	}
}

func TestUpdate(t *testing.T) {
	path := writeFile(t, `{"port": 47821, "providers": {"x": {}}}`)
	c, err := Update(t.Context(), path, func(s *Settings) error {
		s.Lanes = LanesWarn
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Settings.Lanes != LanesWarn {
		t.Fatal("returned config does not hold the change")
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Settings.Lanes != LanesWarn || !reflect.DeepEqual(loaded.Unknown(), []string{"providers"}) {
		t.Fatalf("after Update: lanes=%q unknown=%v", loaded.Settings.Lanes, loaded.Unknown())
	}
}

func TestUpdateCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if _, err := Update(t.Context(), path, func(s *Settings) error { s.Port = 47831; return nil }); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil || !c.Found || c.Settings.Port != 47831 {
		t.Fatalf("Load after Update = %+v, %v", c, err)
	}
}

func TestUpdateRejects(t *testing.T) {
	path := writeFile(t, `{"port": 47821}`)
	errStop := errors.New("stop")
	tests := []struct {
		name    string
		fn      func(*Settings) error
		wantErr error
	}{
		{"callback error", func(*Settings) error { return errStop }, errStop},
		{"invalid change", func(s *Settings) error { s.Port = -1; return nil }, ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Update(t.Context(), path, tt.fn); !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if data, _ := os.ReadFile(path); string(data) != `{"port": 47821}` {
				t.Fatalf("file changed: %s", data)
			}
		})
	}
}

func TestUpdateIsSerialized(t *testing.T) {
	path := writeFile(t, `{"loopRepeat": 2}`)
	const n = 10
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			if _, err := Update(t.Context(), path, func(s *Settings) error {
				s.LoopRepeat++
				return nil
			}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Settings.LoopRepeat != 2+n {
		t.Fatalf("loopRepeat = %d, want %d (lost updates)", c.Settings.LoopRepeat, 2+n)
	}
}

func TestUpdateHonorsContext(t *testing.T) {
	path := writeFile(t, `{}`)
	block := make(chan struct{})
	started := make(chan struct{})
	go func() {
		_, _ = Update(context.Background(), path, func(*Settings) error {
			close(started)
			<-block
			return nil
		})
	}()
	<-started
	defer close(block)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := Update(ctx, path, func(*Settings) error { return nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestPosition(t *testing.T) {
	data := []byte("ab\ncd\nef")
	tests := []struct {
		offset    int64
		line, col int
	}{
		{-1, 1, 1}, {0, 1, 1}, {2, 1, 3}, {3, 2, 1}, {7, 3, 2}, {100, 3, 3},
	}
	for _, tt := range tests {
		if l, c := position(data, tt.offset); l != tt.line || c != tt.col {
			t.Errorf("position(%d) = %d:%d, want %d:%d", tt.offset, l, c, tt.line, tt.col)
		}
	}
}

func TestJSONKind(t *testing.T) {
	for kind, want := range map[string]string{
		"int": "a number", "float64": "a number", "bool": "true or false", "string": "a string",
		"slice": "a list", "struct": "an object", "map": "map",
	} {
		if got := jsonKind(kind); got != want {
			t.Errorf("jsonKind(%q) = %q, want %q", kind, got, want)
		}
	}
}

func TestParseObject(t *testing.T) {
	o, err := parseObject([]byte(`{"b": 1, "a": {"x": [1, 2]}, "b": 3}`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := o.marshal()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"b":3,"a":{"x": [1, 2]}}` {
		t.Fatalf("marshal = %s", out)
	}
	for _, bad := range []string{``, `[`, `{"a":}`, `{"a":1} x`, `{"a" 1}`, `{"a":1`} {
		if _, err := parseObject([]byte(bad)); err == nil {
			t.Errorf("parseObject(%q): want an error", bad)
		}
	}
}
