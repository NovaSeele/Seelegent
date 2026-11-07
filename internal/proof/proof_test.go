package proof

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

func TestMatchAndOverlap(t *testing.T) {
	cases := []struct {
		pattern, file string
		want          bool
	}{
		{"src/", "src/a/b.go", true},
		{"src/*.go", "src/a.go", true},
		{"src/*.go", "src/a/b.go", false},
		{"src/**/*.go", "src/a/b.go", true},
		{"src/**/*.go", "src/b.go", true},
		{"**/x.json", "x.json", true},
		{"a?.txt", "ab.txt", true},
		{"./src/main.go", "src/main.go", true},
		{`src\main.go`, "src/main.go", true},
		{"src/main.go", "src/main.goo", false},
		{"src.go", "srcxgo", false},
	}
	for _, c := range cases {
		if got := Match(c.pattern, c.file); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pattern, c.file, got, c.want)
		}
	}
	// Letter case follows the platform (old bug D13: lower-cased everywhere).
	if got, want := Match("SRC/Main.go", "src/main.go"), runtime.GOOS == "windows" || runtime.GOOS == "darwin"; got != want {
		t.Errorf("case: got %v, want %v", got, want)
	}
	if got := Overlap([]string{"src/a.go", "web/"}, []string{"src/*.go", "docs/"}); !reflect.DeepEqual(got,
		[]PathPair{{Mine: "src/a.go", Theirs: "src/*.go"}}) {
		t.Errorf("Overlap = %v", got)
	}
	if got := Overlap([]string{"src/**"}, []string{"src/x/"}); len(got) != 1 {
		t.Errorf("glob overlap = %v", got)
	}
	if out := Outside([]string{"src/"}, []string{"src/a.go", "README.md"}); !reflect.DeepEqual(out, []string{"README.md"}) {
		t.Errorf("Outside = %v", out)
	}
}

func TestNormalizeLane(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	isDir := func(rel string) bool {
		st, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
		return err == nil && st.IsDir()
	}
	out, bad := NormalizeLane([]string{"src", "./src/", "a.go", " ", "lib/*.go"}, isDir)
	if bad != "" || !reflect.DeepEqual(out, []string{"src/", "a.go", "lib/*.go"}) {
		t.Fatalf("NormalizeLane = %v, %q", out, bad)
	}
	for _, p := range []string{"../x", "/etc/passwd", `C:\x`, "a/../../b"} {
		if _, bad := NormalizeLane([]string{p}, isDir); bad == "" {
			t.Errorf("NormalizeLane accepted %q", p)
		}
	}
}

const tasksMD = "# Tasks\n\n**Feature gate:** `go vet ./...`\n\n" +
	"- [x] T001 [P] [US1] Add parser in `internal/p/parse.go` and `internal/p/` Covers: FR-001, SC-001 Gate: `go test ./internal/p` `go build ./...`\r\n" +
	"- [ ] T002 [US1] Wire `cmd/main.go`, see `spec.md` Covers: FR-002\n" +
	"- [ ] T003 Docs in `README.md` (`-flag` is not a path) Gate: `echo ok`\n" +
	"- [X] T004 Use `Gate:` inside back-quotes Covers: FR-002\n" +
	"* not a task\n"

func TestParseTasks(t *testing.T) {
	ts := ParseTasks(tasksMD)
	if !reflect.DeepEqual(ts.FeatureGate, []string{"go vet ./..."}) {
		t.Errorf("feature gate = %v", ts.FeatureGate)
	}
	if len(ts.Tasks) != 4 {
		t.Fatalf("tasks = %d", len(ts.Tasks))
	}
	t1 := ts.Tasks[0]
	if !t1.Done || !t1.Parallel || t1.Story != "US1" || !reflect.DeepEqual(t1.Covers, []string{"FR-001", "SC-001"}) ||
		!reflect.DeepEqual(t1.Gates, []string{"go test ./internal/p", "go build ./..."}) ||
		!reflect.DeepEqual(t1.PathTokens, []string{"internal/p/parse.go", "internal/p/"}) {
		t.Errorf("T001 = %+v", t1)
	}
	if strings.HasSuffix(t1.Line, "\r") {
		t.Error("the raw line keeps its CR")
	}
	t3 := ts.Tasks[2]
	if len(t3.Covers) != 0 || !reflect.DeepEqual(t3.Gates, []string{"echo ok"}) ||
		!reflect.DeepEqual(t3.PathTokens, []string{"README.md"}) {
		t.Errorf("T003 = %+v", t3)
	}
	if t4 := ts.Tasks[3]; !t4.Done || len(t4.Gates) != 0 {
		t.Errorf("T004 = %+v", t4)
	}
}

func TestResolveLane(t *testing.T) {
	stat := func(rel string) string {
		switch rel {
		case "internal/p":
			return "dir"
		case "README.md", "specs/001/spec.md":
			return "file"
		}
		return ""
	}
	got := ResolveLane([]string{"internal/p", "README.md", "Makefile", "spec.md", "specs/001/plan.md", "cmd/*.go"},
		"specs/001", stat)
	if !reflect.DeepEqual(got, []string{"internal/p/", "README.md", "cmd/*.go"}) {
		t.Errorf("ResolveLane = %v", got)
	}
}

const specMD = "# Feature Specification: Parser\n\n### User Story 1 - Parse things (Priority: P1)\n\n" +
	"- **FR-001**: The parser parses.\n- **FR-002** Errors are reported.\n- **FR-001**: duplicate\n" +
	"- **SC-001**: Fast.\n- **SC-002**: Nobody covers this.\n"

func TestParseSpecAndMatrix(t *testing.T) {
	spec := ParseSpec(specMD)
	if spec.Title != "Parser" || len(spec.Stories) != 1 || spec.Stories[0].ID != "US1" || spec.Stories[0].Priority != "P1" {
		t.Fatalf("spec = %+v", spec)
	}
	if len(spec.Requirements) != 4 || spec.Requirements[1].ID != "FR-002" || spec.Requirements[2].Kind != "SC" {
		t.Fatalf("requirements = %+v", spec.Requirements)
	}
	tasks := ParseTasks(tasksMD)
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	m := BuildMatrix("specs/001", spec, tasks, map[string]Entry{
		"T001": {TaskID: "T001", State: api.GateGreen, At: now, Source: SourceTask, Ref: "t1"},
	})
	status := map[string]api.ProofStatus{}
	for _, r := range m.Rows {
		status[r.ID] = r.Status
	}
	want := map[string]api.ProofStatus{"FR-001": api.ProofProven, "FR-002": api.ProofInProgress,
		"SC-001": api.ProofProven, "SC-002": api.ProofUnmapped}
	if !reflect.DeepEqual(status, want) {
		t.Errorf("status = %v", status)
	}
	if m.Counts.Proven != 2 || m.Counts.InProgress != 1 || m.Counts.Unmapped != 1 || m.Tasks.Total != 4 || m.Tasks.Done != 2 {
		t.Errorf("counts = %+v tasks %+v", m.Counts, m.Tasks)
	}
	if !reflect.DeepEqual(m.UncoveredTasks, []string{"T003"}) || m.Stories[0].Counts.Proven != 2 {
		t.Errorf("uncovered %v stories %+v", m.UncoveredTasks, m.Stories)
	}
	// Regressed beats failing; failing beats unproven.
	red := Entry{TaskID: "T001", State: api.GateRed, Source: SourceRegression, LastGreenAt: now}
	if got := status2(spec, tasks, red); got != api.ProofRegressed {
		t.Errorf("regressed: %s", got)
	}
	red.LastGreenAt = time.Time{}
	if got := status2(spec, tasks, red); got != api.ProofFailing {
		t.Errorf("failing: %s", got)
	}
	// Without a gate result the ticked task is unproven.
	m = BuildMatrix("f", spec, tasks, nil)
	if m.Rows[0].Status != api.ProofUnproven {
		t.Errorf("unproven: %s", m.Rows[0].Status)
	}
}

func status2(spec Spec, tasks Tasks, e Entry) api.ProofStatus {
	return BuildMatrix("f", spec, tasks, map[string]Entry{"T001": e}).Rows[0].Status
}

func TestDoctrine(t *testing.T) {
	checks, err := ParseDoctrine([]byte(`{"version":1,"checks":[{"id":"vet","run":"go vet ./..."},` +
		`{"id":"web","run":"pnpm lint","when":["web/**"]}]}`))
	if err != nil || len(checks) != 2 {
		t.Fatalf("ParseDoctrine = %v, %v", checks, err)
	}
	if got := DoctrineFor(checks, []string{"main.go"}); len(got) != 1 || got[0].ID != "vet" {
		t.Errorf("DoctrineFor = %v", got)
	}
	if got := DoctrineFor(checks, []string{"web/src/a.ts"}); len(got) != 2 {
		t.Errorf("DoctrineFor web = %v", got)
	}
	for _, bad := range []string{`{}`, `{"version":2,"checks":[]}`, `{"version":1}`, `[]`,
		`{"version":1,"checks":[{"id":"a"}]}`, `{"version":1,"checks":[{"id":"a","run":"x"},{"id":"a","run":"y"}]}`,
		`{"version":1,"checks":[{"id":"a","run":"x","when":[""]}]}`} {
		if _, err := ParseDoctrine([]byte(bad)); err == nil {
			t.Errorf("ParseDoctrine(%s) accepted", bad)
		}
	}
	dir := t.TempDir()
	if c, err := ReadDoctrine(dir); err != nil || c != nil {
		t.Errorf("no doctrine: %v, %v", c, err)
	}
}

func TestBar(t *testing.T) {
	root := t.TempDir()
	feature := filepath.Join(root, "specs", "001")
	write(t, filepath.Join(feature, "spec.md"), "spec")
	write(t, filepath.Join(feature, "contracts", "api.md"), "api")
	write(t, filepath.Join(root, ".seelegent", "doctrine.json"), "{}")
	lines := map[string]string{"T001": "- [ ] T001 do it"}
	b := SnapshotBar(root, feature, lines, []string{"go test"})
	for _, k := range []string{"gate", "spec.md", "contracts/api.md", DoctrineFile, "tasks.md#T001"} {
		if b[k] == "" {
			t.Errorf("bar misses %s: %v", k, b)
		}
	}
	// Ticking a task does not change the bar; editing a contract does.
	lines["T001"] = "- [x] T001 do it"
	write(t, filepath.Join(feature, "contracts", "api.md"), "api v2")
	if d := b.Diff(SnapshotBar(root, feature, lines, []string{"go test"})); !reflect.DeepEqual(d, []string{"contracts/api.md"}) {
		t.Errorf("Diff = %v", d)
	}
	if d := b.Diff(SnapshotBar(root, feature, lines, []string{"go test", "x"})); len(d) != 2 {
		t.Errorf("Diff gate = %v", d)
	}
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckCite(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a", "b.go"), "package a\n\nfunc   Hello() {\n}\n")
	cases := []struct {
		c    Cite
		want string
	}{
		{Cite{Path: "a/b.go", Line: 3}, CiteLocated},
		{Cite{Path: "./a/b.go", Line: 2, EndLine: 4, Quote: "func Hello() {"}, CiteVerified},
		{Cite{Path: "a/b.go", Line: 3, Quote: "func Bye"}, CiteQuoteMismatch},
		{Cite{Path: "a/b.go", Line: 9}, CiteBadRange},
		{Cite{Path: "a/b.go", Line: 0}, CiteBadRange},
		{Cite{Path: "a/c.go", Line: 1}, CiteMissingFile},
		{Cite{Path: "../x.go", Line: 1}, CiteOutsideRepo},
		{Cite{Path: "a", Line: 1}, CiteMissingFile},
	}
	for _, c := range cases {
		if got := CheckCite(root, c.c); got.State != c.want {
			t.Errorf("CheckCite(%+v) = %s (%s), want %s", c.c, got.State, got.Detail, c.want)
		}
	}
	cites := FindCites("- internal/a.go:12 - nil map\n- see (web/x.ts:3-5) and internal/a.go:12 again; v1.2 is not one")
	if !reflect.DeepEqual(cites, []Cite{{Path: "internal/a.go", Line: 12}, {Path: "web/x.ts", Line: 3, EndLine: 5}}) {
		t.Errorf("FindCites = %+v", cites)
	}
	if s := Summarize([]CiteResult{{State: CiteVerified}, {State: CiteBadRange}}); s.Total != 2 || s.Unverified != 1 {
		t.Errorf("Summarize = %+v", s)
	}
}
