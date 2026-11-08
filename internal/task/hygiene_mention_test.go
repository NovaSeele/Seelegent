package task

import "testing"

// The unreported_file check counts a new file as mentioned when the report names its path, its base name, its stem
// as a whole word, or one of its folders written as a path (field report: 34 warnings for grouped lists).
func TestReportMentions(t *testing.T) {
	report := "## Summary\n" +
		"- `src/components/keyboard/`: KeyboardView, Keycap, SelectionBar\n" +
		"- `src/pages/`: PerformancePage plus 8 placeholder pages\n" +
		"- Wired the routes in src\\router and the Application shell; see docs/guide.md.\n" +
		"- Tests under tests/unit pass. Touched the src folder, other/lib/ and README.\n"
	cases := []struct {
		rel  string
		want bool
	}{
		{"docs/guide.md", true},                      // (a) full path
		{"notes/README", true},                       // (b) base name
		{"src/components/keyboard/Keycap.vue", true}, // (c) stem, as a whole word and in another case
		{"lib/keyboardview.ts", true},                // (c) stem
		{"src/pages/AboutPage.vue", true},            // (d) "src/pages/"
		{"src/pages/deep/Thing.vue", true},           // (d) an ancestor further up
		{"src/router/index.ts", true},                // (d) two segments without a slash, backslashes normalised
		{"tests/unit/a.spec.ts", true},               // (d) two segments without a slash
		{"src/App.vue", false},                       // the stem only inside "Application"; "src" bare
		{"src/Key.vue", false},                       // the stem only inside "Keycap" / "KeyboardView"
		{"src/ab.ts", false},                         // a stem under 3 characters
		{"lib/x.ts", false},                          // "other/lib/" is another folder
		{"src/tests/x.ts", false},                    // "tests/unit" names tests/unit, not src/tests
		{"pages/Other.vue", false},                   // "src/pages/" is not "pages/" at the root
	}
	m := newMentions(report)
	for _, c := range cases {
		if got := m.names(c.rel); got != c.want {
			t.Errorf("names(%q) = %v, want %v", c.rel, got, c.want)
		}
	}
	if !newMentions("Added the `assets/` folder.").names("assets/logo.svg") {
		t.Error("a single folder written with its slash should count")
	}
}
