package answer

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/proof"
)

const sample = "Preamble is dropped.\n## **Position**\nB\n\n### Claims:\n" +
	"- The cache never expires [src/cache.go:12-14] \"ttl := 0\"\n" +
	"  and nobody resets it.\n" +
	"* second [README.md:3]\n\n" +
	"## Questions for the lead\n- none\n"

func TestSectionsBulletsClaims(t *testing.T) {
	secs := Sections(sample)
	if len(secs) != 3 || secs[0].Title != "position" || secs[1].Title != "claims" {
		t.Fatalf("sections = %+v", secs)
	}
	claims := Claims(secs[1].Body)
	if len(claims) != 2 {
		t.Fatalf("claims = %+v", claims)
	}
	c := claims[0]
	if c.Text != "The cache never expires and nobody resets it." || len(c.Cites) != 1 {
		t.Errorf("claim = %+v", c)
	}
	if ci := c.Cites[0]; ci.Path != "src/cache.go" || ci.Line != 12 || ci.EndLine != 14 || ci.Quote != "ttl := 0" {
		t.Errorf("cite = %+v", ci)
	}
	if q := Questions(secs[2].Body); len(q) != 0 {
		t.Errorf("questions = %v", q)
	}
}

func TestCheckClaims(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\nvar x = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := CheckClaims(root, Claims("- x is one [a.go:2] \"var x = 1\"\n- y [a.go:2] \"var y\"\n- z [b.go:1]\n"))
	v, l, u := Counts(got)
	if v != 1 || l != 0 || u != 2 {
		t.Errorf("counts = %d %d %d: %+v", v, l, u, got)
	}
	if got[1].Cites[0].State != proof.CiteQuoteMismatch || CiteTag(got[2].Cites[0]) != "(UNVERIFIED: missing_file)" {
		t.Errorf("states = %+v", got)
	}
	if s := CiteText(got[0].Cites[0]); s != `[a.go:2] "var x = 1"` {
		t.Errorf("cite text = %s", s)
	}
}

// Old bug D20: identity words that are ordinary words were blanked everywhere.
func TestRedactorKeepsOrdinaryWords(t *testing.T) {
	r := Redactor([]Identity{
		{Member: "api", Name: "The", Backend: "agy", Model: "gemini-3.8-flash", Tier: "max"},
		{Member: "nova-reviewer", Name: "Nova", Backend: "codex", Tier: "fast"},
	})
	in := "The api is fast; max out the cache. nova-reviewer said Nova uses gemini-3.8-flash via Codex and Claude."
	want := "The api is fast; max out the cache. [redacted] said [redacted] uses [redacted] via [redacted] and [redacted]."
	if got := r(in); got != want {
		t.Errorf("redacted:\n got %s\nwant %s", got, want)
	}
	if got := r("novak and supernova"); got != "novak and supernova" {
		t.Errorf("partial words: %s", got)
	}
}

func TestClip(t *testing.T) {
	if Clip("héllo", 3) != "hél…" || Clip("ok", 3) != "ok" {
		t.Error("clip")
	}
	if !slices.Contains(BrandWords(), "Gemini") {
		t.Error("brands")
	}
}
