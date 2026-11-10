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

// Demo bug: a consult's required change "set CORS_ORIGINS" read "set CORSORIGINS" because every '_' was dropped.
func TestStripInline(t *testing.T) {
	cases := []struct{ in, want string }{
		{"CORS_ORIGINS", "CORS_ORIGINS"},
		{"set CORS_ORIGINS to the dashboard origin", "set CORS_ORIGINS to the dashboard origin"},
		{"snake_case_name", "snake_case_name"},
		{"_emphasis_", "emphasis"},
		{"an _emphatic_ word", "an emphatic word"},
		{"`MAX_RETRY_COUNT`", "MAX_RETRY_COUNT"},
		{"rename `old_name` to `new_name`", "rename old_name to new_name"},
		{"`_x_ and **y**`", "_x_ and **y**"},
		{"`` a `b` c ``", "a `b` c"},
		{"**Set** CORS_ORIGINS in `config_file.go`", "Set CORS_ORIGINS in config_file.go"},
		{"*one* and ***both***", "one and both"},
		{"2 * 3 * 4", "2 * 3 * 4"},
		{"a lone _ underscore and _unclosed", "a lone _ underscore and _unclosed"},
		{"an `unclosed span with_underscore", "an `unclosed span with_underscore"},
		{"`code` then _em_", "code then em"},
		{"plain text", "plain text"},
	}
	for _, c := range cases {
		if got := StripInline(c.in); got != c.want {
			t.Errorf("StripInline(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestQuestionsKeepIdentifiers(t *testing.T) {
	body := "- Set `CORS_ORIGINS` and DB_POOL_SIZE in **config.go**\n- rename snake_case_name\n- _none_\n"
	got := Questions(body)
	want := []string{"Set CORS_ORIGINS and DB_POOL_SIZE in config.go", "rename snake_case_name"}
	if !slices.Equal(got, want) {
		t.Errorf("questions = %q, want %q", got, want)
	}
	if secs := Sections("## __Required_changes__\n- x\n"); len(secs) != 1 || secs[0].Title != "required_changes" {
		t.Errorf("sections = %+v", secs)
	}
}
