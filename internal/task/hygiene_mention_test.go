package task

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/snapshot"
)

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

// A worker in a subfolder (cwd app/) writes its paths from there: "src/pages/" names app/src/pages/Contact.vue.
// Files are relative to the repository root, so the form relative to the task's folder is tried too.
func TestReportMentionsUnderTaskFolder(t *testing.T) {
	m := newMentions("- `src/pages/`: the new pages\n- wired lib/router\n")
	cases := []struct {
		rel, cwdRel string
		want        bool
	}{
		{"app/src/pages/Contact.vue", "app", true},
		{"app/lib/router/index.ts", "app", true},
		{"app/src/pages/Contact.vue", "", false},      // the task works at the root
		{"other/src/pages/Contact.vue", "app", false}, // outside the task's folder
		{"application/src/pages/x.vue", "app", false}, // "app" is not a prefix of "application/"
	}
	for _, c := range cases {
		if got := m.namesUnder(c.rel, c.cwdRel); got != c.want {
			t.Errorf("namesUnder(%q, %q) = %v, want %v", c.rel, c.cwdRel, got, c.want)
		}
	}

	// The whole check, with the task's folder app/ inside the repository.
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "init", "-q", "-b", "main")
	gitIn(t, root, "config", "user.name", "Test")
	gitIn(t, root, "config", "user.email", "test@example.invalid")
	gitIn(t, root, "config", "commit.gpgsign", "false")
	put := func(name string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := writeText(p, "x\n"); err != nil {
			t.Fatal(err)
		}
	}
	put("app/package.json")
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-q", "-m", "init")
	repos := gitx.New(time.Now, nil)
	s := &Service{o: Options{Snapshots: snapshot.New(filepath.Join(t.TempDir(), "snapshots"), repos, nil),
		Repos: repos}}
	ctx := t.Context()
	start, err := s.o.Snapshots.Take(ctx, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	put("app/src/pages/Contact.vue")
	put("app/src/stray.ts")
	end, err := s.o.Snapshots.Take(ctx, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	changed, _ := changedFiles(start, end, nil, nil)
	unreported := func(cwd string) []string {
		var out []string
		for _, f := range s.hygiene(ctx, root, cwd, start, end, changed, "Added `src/pages/`.", true,
			func(string) bool { return true }) {
			if f.Kind == api.FindingUnreported {
				out = append(out, f.Path)
			}
		}
		return out
	}
	if got := unreported(filepath.Join(root, "app")); !slices.Equal(got, []string{"app/src/stray.ts"}) {
		t.Fatalf("cwd app/: unreported %v, want only app/src/stray.ts", got)
	}
	if got := unreported(root); len(got) != 2 {
		t.Fatalf("cwd at the root: unreported %v, want both files", got)
	}
}

// unreported_file judges only files the worker plausibly made: its edit tools' files and the paths its shell
// commands write. A file someone else wrote during the turn (the lead editing by hand) is in the snapshots but is
// not the report's to name (field report: the lead's own files flagged in a worker's task).
func TestUnreportedOnlyForWorkerFiles(t *testing.T) {
	h := newHarness(t)
	h.initRepo(map[string]string{"a.txt": "a\n"})
	h.agy("w", 1)
	cwd := h.cwd
	h.queue.Push(t, script(agyInit("conv-h"),
		agyEdit(1, filepath.Join(cwd, "src", "tool.ts")),
		fakeWrite(filepath.Join(cwd, "src", "tool.ts"), "x\n"),
		agyTool(2, "DONE", "echo x > new.txt"),
		fakeWrite(filepath.Join(cwd, "new.txt"), "x\n"),
		fakeWrite(filepath.Join(cwd, "src", "protocol", "safety.ts"), "the lead's\n"), // an outside write
		agyResult("conv-h", "Done.")))
	sum := h.delegate(api.DelegateRequest{Title: "authors"})
	tk := h.waitStatus(sum.ID, StatusDone)
	tu := tk.LastTurn()
	if tu.Snap == nil || !tu.Snap.Checked {
		t.Fatalf("snapshots: %+v", tu.Snap)
	}
	var unreported []string
	for _, f := range tu.Hygiene {
		if f.Kind == api.FindingUnreported {
			unreported = append(unreported, f.Path)
		}
	}
	slices.Sort(unreported)
	if want := []string{"new.txt", "src/tool.ts"}; !slices.Equal(unreported, want) {
		t.Fatalf("unreported %v, want %v (hygiene %+v)", unreported, want, tu.Hygiene)
	}
	if !slices.Contains(tu.Files, "src/protocol/safety.ts") {
		t.Fatalf("the outside write is still among the turn's files: %v", tu.Files)
	}
}

// workerWrites reads the paths of the shell commands against the task's folder, and a folder a command makes
// holds the worker's files; the task's folder itself claims nothing.
func TestWorkerWrites(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "app")
	w := newWorkerWrites(cwd, root, []string{"app/src/edited.ts"},
		[]string{"echo x > out/log.txt", "mkdir gen", "cp -r tpl .", "echo y > ../top.txt"})
	for rel, want := range map[string]bool{
		"app/src/edited.ts": true,
		"app/out/log.txt":   true,
		"app/gen/a/b.ts":    true,
		"top.txt":           true,
		"app/other.ts":      false, // under the task's folder, which "cp -r tpl ." names
		"app/out/other.txt": false,
	} {
		if got := w.wrote(rel); got != want {
			t.Errorf("wrote(%q) = %v, want %v", rel, got, want)
		}
	}
	if !slices.Equal(w.unknown, []string{"cp -r tpl ."}) {
		t.Errorf("unknown = %q", w.unknown)
	}
}

// A worker cut off before its report (a crash, a quota stop) names nothing: its new files are not unreported_file
// warnings, while the other hygiene checks still run.
func TestNoUnreportedFilesWithoutAReport(t *testing.T) {
	h := newHarness(t)
	h.initRepo(map[string]string{"a.txt": "a\n"})
	h.agy("only", 0)
	cwd := h.cwd
	h.queue.Push(t, script(agyInit("conv-x"),
		agyEdit(1, filepath.Join(cwd, "src", "new.ts")),
		fakeWrite(filepath.Join(cwd, "src", "new.ts"), "x\n"),
		fakeWrite(filepath.Join(cwd, "left.orig"), "x\n"),
		"#!stderr fatal: the worker process crashed", "#!exit 1"))
	sum := h.delegate(api.DelegateRequest{})
	tk := h.waitStatus(sum.ID, StatusFailed)
	tu := tk.LastTurn()
	if tu.Snap == nil || !tu.Snap.Checked {
		t.Fatalf("snapshots: %+v", tu.Snap)
	}
	if len(tu.Hygiene) != 1 || tu.Hygiene[0].Kind != api.FindingJunkFile || tu.Hygiene[0].Path != "left.orig" {
		t.Fatalf("hygiene: %+v", tu.Hygiene)
	}
	if !slices.Contains(tu.Files, "src/new.ts") {
		t.Fatalf("files: %v", tu.Files)
	}
}
