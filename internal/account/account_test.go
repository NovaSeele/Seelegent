package account

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/store"
)

// fixture is a manager on a fresh database and temp directories.
type fixture struct {
	m        *Manager
	db       *store.Store
	profiles string
	realHome string
	clock    *fakeClock
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time {
	c.t = c.t.Add(time.Second)
	return c.t
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "seelegent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := &fixture{
		db:       db,
		profiles: filepath.Join(dir, "profiles"),
		realHome: filepath.Join(dir, "realhome"),
		clock:    &fakeClock{t: time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)},
	}
	for _, d := range []string{f.profiles, f.realHome} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f.m = NewManager(db, NewProfiles(f.profiles, f.realHome), f.clock.now)
	return f
}

func ctxT(t *testing.T) context.Context {
	t.Helper()
	return t.Context()
}

func (f *fixture) provider(t *testing.T, id, preset string) Provider {
	t.Helper()
	p, err := FromPreset(id, preset)
	if err != nil {
		t.Fatal(err)
	}
	if p.BaseURL == "" && !p.Official {
		p.BaseURL = "https://llm.example.test/v1"
	}
	p.Tiers = config.TierModels{Fast: "model-fast", Standard: "model-std"}
	p, err = f.m.CreateProvider(ctxT(t), p)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNewValidate(t *testing.T) {
	long := strings.Repeat("x", maxLabel+1)
	cases := []struct {
		name string
		n    New
		ok   bool
	}{
		{"agy isolated", New{Backend: "agy", Kind: KindIsolated, Label: "a"}, true},
		{"agy system", New{Backend: "agy", Kind: KindSystem}, true},
		{"claude api", New{Backend: "claude", Kind: KindAPIKey, Provider: "p"}, true},
		{"unknown backend", New{Backend: "gpt", Kind: KindIsolated}, false},
		{"claude system", New{Backend: "claude", Kind: KindSystem}, false},
		{"agy api", New{Backend: "agy", Kind: KindAPIKey, Provider: "p"}, false},
		{"api without provider", New{Backend: "codex", Kind: KindAPIKey}, false},
		{"provider on isolated", New{Backend: "codex", Kind: KindIsolated, Provider: "p"}, false},
		{"bad kind", New{Backend: "codex", Kind: "web"}, false},
		{"long label", New{Backend: "agy", Kind: KindIsolated, Label: long}, false},
		{"control label", New{Backend: "agy", Kind: KindIsolated, Label: "a\nb"}, false},
		{"invalid utf8", New{Backend: "agy", Kind: KindIsolated, Label: "\xff"}, false},
		{"priority", New{Backend: "agy", Kind: KindIsolated, Priority: maxPriority + 1}, false},
	}
	for _, c := range cases {
		err := c.n.validate()
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok=%v", c.name, err, c.ok)
		}
		if err != nil && !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v does not match ErrInvalid", c.name, err)
		}
	}
}

func TestValidID(t *testing.T) {
	for _, id := range []string{"acc1", "system", "a-b_c"} {
		if !validID(id) {
			t.Errorf("validID(%q) = false", id)
		}
	}
	for _, id := range []string{"", ".", "..", "../x", `a\b`, "A", "a/b", strings.Repeat("a", 65)} {
		if validID(id) {
			t.Errorf("validID(%q) = true", id)
		}
	}
}

func TestProviderValidate(t *testing.T) {
	ok := func(p Provider) Provider {
		t.Helper()
		if err := p.Validate(); err != nil {
			t.Fatalf("%+v: %v", p, err)
		}
		return p
	}
	bad := func(p Provider, want string) {
		t.Helper()
		err := p.Validate()
		if err == nil || !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: err = %v, want %q", p, err, want)
		}
	}
	p := ok(Provider{ID: "anthropic", Backend: "claude", Official: true, BaseURL: "https://ignored.test"})
	if p.BaseURL != "" || p.Name != "anthropic" {
		t.Errorf("official provider not normalised: %+v", p)
	}
	p = ok(Provider{ID: "x", Name: " X ", Backend: "codex", BaseURL: " https://a.test/v1 "})
	if p.WireAPI != WireChat || p.Name != "X" || p.BaseURL != "https://a.test/v1" {
		t.Errorf("codex provider not normalised: %+v", p)
	}
	ok(Provider{ID: "local", Backend: "codex", BaseURL: "http://127.0.0.1:11434/v1", WireAPI: WireResponses})
	ok(Provider{ID: "t", Backend: "claude", BaseURL: "https://a.test", AllowedModels: "^glm-",
		Tiers: config.TierModels{Fast: "GLM-4"}})

	bad(Provider{ID: "Bad", Backend: "claude", BaseURL: "https://a.test"}, "provider id")
	bad(Provider{ID: "x", Backend: "agy", BaseURL: "https://a.test"}, "backend")
	bad(Provider{ID: "x", Backend: "claude", BaseURL: "https://a.test", WireAPI: "chat"}, "only for codex")
	bad(Provider{ID: "x", Backend: "codex", Official: true}, "official")
	bad(Provider{ID: "x", Backend: "codex", BaseURL: "https://a.test", WireAPI: "grpc"}, "wireApi")
	bad(Provider{ID: "x", Backend: "claude", BaseURL: "https://a.test", Preset: "nope"}, "preset")
	bad(Provider{ID: "x", Backend: "claude"}, "required")
	bad(Provider{ID: "x", Backend: "claude", BaseURL: "http://evil.test"}, "localhost")
	bad(Provider{ID: "x", Backend: "claude", BaseURL: "ftp://a.test"}, "https")
	bad(Provider{ID: "x", Backend: "claude", BaseURL: "https://u:p@a.test"}, "credentials")
	bad(Provider{ID: "x", Backend: "claude", BaseURL: "::"}, "valid URL")
	bad(Provider{ID: "x", Backend: "claude", BaseURL: "https://a.test", AllowedModels: "("}, "regular expression")
	// The tier models are allowed on top of the allow-list, so they need not match it.
	ok(Provider{ID: "x", Backend: "claude", BaseURL: "https://a.test", AllowedModels: "^glm",
		Tiers: config.TierModels{Max: "kimi"}})
	// A list is cleaned and becomes the pattern; it wins over a stale pattern.
	p = ok(Provider{ID: "ds", Backend: "claude", BaseURL: "https://a.test", AllowedModels: "^old-",
		Models: []string{" deepseek-v4 ", "", "deepseek-r2", "deepseek-v4"}})
	if !slices.Equal(p.Models, []string{"deepseek-v4", "deepseek-r2"}) ||
		p.AllowedModels != `^(?:deepseek-v4|deepseek-r2)$` {
		t.Errorf("list not applied: %+v", p)
	}
	bad(Provider{ID: "x", Backend: "claude", BaseURL: "https://a.test", Models: []string{"a b"}}, "spaces")
	bad(Provider{ID: "x", Backend: "claude", BaseURL: "https://a.test", Tiers: config.TierModels{Max: "a b"}},
		"spaces")
	bad(Provider{ID: "x", Name: "a\x00", Backend: "claude", BaseURL: "https://a.test"}, "control")
}

func TestPresets(t *testing.T) {
	for id, pr := range Presets() {
		p, err := FromPreset("p-"+id, id)
		if err != nil {
			t.Fatal(err)
		}
		if p.Backend != pr.Backend || p.Preset != id || !p.Enabled {
			t.Errorf("%s: %+v", id, p)
		}
		if p.BaseURL == "" && !p.Official {
			p.BaseURL = "https://custom.test"
		}
		if err := p.Validate(); err != nil {
			t.Errorf("preset %s does not validate: %v", id, err)
		}
	}
	if _, err := FromPreset("x", "nope"); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown preset: %v", err)
	}
}

func TestModelsPatternRoundTrip(t *testing.T) {
	for _, list := range [][]string{{"deepseek-v4", "deepseek-r2"}, {"gpt-5.1"}, {"a|b", `c\d`, "(x)"}} {
		pat := ModelsPattern(list)
		if got := ModelsOf(pat); !slices.Equal(got, list) {
			t.Errorf("ModelsOf(ModelsPattern(%q)) = %q (pattern %q)", list, got, pat)
		}
		re := regexp.MustCompile("(?i)" + pat)
		for _, m := range list {
			if !re.MatchString(m) || re.MatchString(m+"x") {
				t.Errorf("pattern %q and model %q", pat, m)
			}
		}
	}
	// Hand-written patterns are not lists.
	for _, pat := range []string{"", "^glm-", "^(?:a.b)$", "^(?:a)$x", `^(?:a\)$`, "^(?:)$"} {
		if got := ModelsOf(pat); got == nil || len(got) != 0 {
			t.Errorf("ModelsOf(%q) = %q, want []", pat, got)
		}
	}
	if ModelsPattern(nil) != "" {
		t.Error("an empty list must be the empty pattern")
	}
}
