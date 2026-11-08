package task

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/snapshot"
)

// eolCase is one turn in a throwaway repository: committed files, files re-checked out through git (so the
// checkout filters write them), uncommitted changes before the turn and the turn's writes.
type eolCase struct {
	name     string
	autocrlf string
	commit   map[string]string
	checkout []string
	// checkoutCRLF: the checkout must have written CRLF (the case is about that form).
	checkoutCRLF bool
	before       map[string]string
	turn         map[string]string
	// appendSame are files the turn adds a line to in the line ending the file already has.
	appendSame []string
	want       map[string][]string // path -> finding kinds
}

const eolLF, eolCRLF = "a\nb\nc\n", "a\r\nb\r\nc\r\n"

// The line endings and BOM check judges what the worker changed, not what a fresh checkout would write (M18: an LF
// working copy under core.autocrlf=true was flagged against the CRLF checkout form, and the fix turns could not
// clear it).
func TestHygieneLineEndingsJudgeTheTurn(t *testing.T) {
	lf, crlf := eolLF, eolCRLF
	bom := string(utf8BOM)
	for _, tc := range []eolCase{
		{name: "autocrlf, LF working copy edited with LF", autocrlf: "true",
			commit: map[string]string{"calc.py": lf, "test_calc.py": lf},
			turn:   map[string]string{"calc.py": lf + "d\n", "test_calc.py": "x\n" + lf}},
		{name: "autocrlf, checked-out CRLF working copy edited keeping CRLF", autocrlf: "true",
			commit: map[string]string{"calc.py": lf}, checkout: []string{"calc.py"}, checkoutCRLF: true,
			turn: map[string]string{"calc.py": crlf + "d\r\n"}},
		{name: "autocrlf, LF working copy saved as CRLF", autocrlf: "true",
			commit: map[string]string{"calc.py": lf}, turn: map[string]string{"calc.py": crlf + "d\r\n"}},
		// git stores every line as LF: the mix never reaches a commit.
		{name: "autocrlf, LF working copy with mixed lines", autocrlf: "true",
			commit: map[string]string{"calc.py": lf + lf}, turn: map[string]string{"calc.py": lf + lf + "d\r\n"}},
		{name: "no autocrlf, LF file gets CRLF lines", autocrlf: "false",
			commit: map[string]string{"calc.py": lf}, turn: map[string]string{"calc.py": lf + "d\r\ne\r\n"},
			want: map[string][]string{"calc.py": {api.FindingEOLChanged}}},
		{name: "no autocrlf, CRLF file gets bare LF lines", autocrlf: "false",
			commit: map[string]string{"calc.py": crlf}, turn: map[string]string{"calc.py": crlf + "d\ne\n"},
			want: map[string][]string{"calc.py": {api.FindingEOLChanged}}},
		{name: "BOM added", autocrlf: "false",
			commit: map[string]string{"calc.py": lf}, turn: map[string]string{"calc.py": bom + lf + "d\n"},
			want: map[string][]string{"calc.py": {api.FindingBOMChanged}}},
		{name: "BOM removed", autocrlf: "true",
			commit: map[string]string{"calc.py": bom + lf}, turn: map[string]string{"calc.py": lf + "d\n"},
			want: map[string][]string{"calc.py": {api.FindingBOMChanged}}},
		{name: "start style differs from HEAD and is kept", autocrlf: "false",
			commit: map[string]string{"calc.py": lf, "b.txt": lf},
			before: map[string]string{"calc.py": crlf, "b.txt": bom + lf},
			turn:   map[string]string{"calc.py": crlf + "d\r\n", "b.txt": bom + lf + "d\n"}},
		{name: "start style differs from HEAD and the turn restores HEAD's", autocrlf: "false",
			commit: map[string]string{"calc.py": lf}, before: map[string]string{"calc.py": crlf},
			turn: map[string]string{"calc.py": lf + "d\n"}},
		{name: "dirty LF file gets CRLF lines", autocrlf: "false",
			commit: map[string]string{"calc.py": lf}, before: map[string]string{"calc.py": lf + "d\n"},
			turn: map[string]string{"calc.py": lf + "d\r\ne\r\n"},
			want: map[string][]string{"calc.py": {api.FindingEOLChanged}}},
	} {
		t.Run(tc.name, func(t *testing.T) { checkEOLCase(t, tc) })
	}
}

// Every core.autocrlf setting with and without line ending attributes: a file kept in the style git gave it or the
// worker wrote is never a finding; mixed line endings are one only where git stores them as they are; a BOM change
// always is.
func TestHygieneLineEndingsMatrix(t *testing.T) {
	lf, crlf := eolLF, eolCRLF
	attrs := []struct{ name, text string }{
		{"no attributes", ""},
		{"text=auto", "* text=auto\n"},
		{"eol=crlf", "* text eol=crlf\n"},
		{"eol=lf", "* text eol=lf\n"},
	}
	for _, autocrlf := range []string{"true", "false", "input"} {
		for _, a := range attrs {
			commit := map[string]string{"keep.py": lf, "lf.py": lf, "crlf.py": crlf, "mixed.py": lf,
				"crlfmix.py": crlf, "bom.py": lf}
			if a.text != "" {
				commit[".gitattributes"] = a.text
			}
			want := map[string][]string{"bom.py": {api.FindingBOMChanged}}
			if autocrlf == "false" && a.text == "" { // nothing normalizes: the working copy is what gets committed
				want["mixed.py"] = []string{api.FindingEOLChanged}
				want["crlfmix.py"] = []string{api.FindingEOLChanged}
			}
			tc := eolCase{name: "autocrlf=" + autocrlf + ", " + a.name, autocrlf: autocrlf, commit: commit,
				checkout: []string{"keep.py"}, appendSame: []string{"keep.py", "lf.py", "crlf.py"},
				turn: map[string]string{"mixed.py": lf + "d\r\ne\r\n", "crlfmix.py": crlf + "d\ne\n",
					"bom.py": string(utf8BOM) + lf}, want: want}
			t.Run(tc.name, func(t *testing.T) { checkEOLCase(t, tc) })
		}
	}
}

func checkEOLCase(t *testing.T, tc eolCase) {
	t.Helper()
	got := runEOLCase(t, tc)
	want := tc.want
	if want == nil {
		want = map[string][]string{}
	}
	if len(got) != len(want) {
		t.Fatalf("findings %v, want %v", got, want)
	}
	for p, kinds := range want {
		if !slices.Equal(got[p], kinds) {
			t.Fatalf("findings %v, want %v", got, want)
		}
	}
}

// runEOLCase sets the case up in a new repository and returns the hygiene findings of its turn by path.
func runEOLCase(t *testing.T, tc eolCase) map[string][]string {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "repo")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "init", "-q", "-b", "main")
	gitIn(t, root, "config", "user.name", "Test")
	gitIn(t, root, "config", "user.email", "test@example.invalid")
	gitIn(t, root, "config", "commit.gpgsign", "false")
	gitIn(t, root, "config", "core.autocrlf", tc.autocrlf)
	gitIn(t, root, "config", "core.safecrlf", "false")
	write := func(files map[string]string) {
		for name, text := range files {
			if err := writeText(filepath.Join(root, name), text); err != nil {
				t.Fatal(err)
			}
		}
	}
	read := func(name string) []byte {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	write(tc.commit)
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-q", "-m", "init")
	for _, name := range tc.checkout {
		if err := os.Remove(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
		gitIn(t, root, "checkout", "--", name)
		if data := read(name); tc.checkoutCRLF && !bytes.Contains(data, []byte("\r\n")) {
			t.Fatalf("checkout of %s did not write CRLF: %q", name, data)
		}
	}
	write(tc.before)

	repos := gitx.New(time.Now, nil)
	s := &Service{o: Options{Snapshots: snapshot.New(filepath.Join(dir, "snapshots"), repos, nil), Repos: repos}}
	ctx := t.Context()
	start, err := s.o.Snapshots.Take(ctx, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	turn := map[string]string{}
	for name, text := range tc.turn {
		turn[name] = text
	}
	for _, name := range tc.appendSame {
		data := string(read(name))
		eol := "\n"
		if strings.Contains(data, "\r\n") {
			eol = "\r\n"
		}
		turn[name] = data + "z" + eol
	}
	write(turn)
	edited := map[string]bool{}
	var extra []string
	for name := range turn {
		edited[snapshot.Key(name)] = true
		extra = append(extra, name)
	}
	for _, f := range start.Files {
		extra = append(extra, f.Path)
	}
	end, err := s.o.Snapshots.Take(ctx, root, extra)
	if err != nil {
		t.Fatal(err)
	}
	changed, _ := changedFiles(start, end, edited, nil)
	if len(changed) != len(turn) {
		t.Fatalf("changed %v, the turn wrote %d files", changed, len(turn))
	}
	got := map[string][]string{}
	for _, f := range s.hygiene(ctx, root, start, end, changed, "changed the files") {
		got[f.Path] = append(got[f.Path], f.Kind)
	}
	return got
}

// An automatic fix turn that leaves the same line ending findings ends the fix loop at once: the lead decides,
// the rest of the budget is not spent (M18: two fix turns could not clear a finding). The steps the findings stopped
// still run, and pass.
func TestGateStopsFixLoopOnSameStyleFindings(t *testing.T) {
	h := newHarness(t)
	h.set(func(s *config.Settings) { s.GateMaxAutoFixes = 2 })
	h.initRepo(map[string]string{"a.txt": "x\ny\nz\n", "b.txt": "b\n"})
	h.agy("w", 1)
	a, b := filepath.Join(h.cwd, "a.txt"), filepath.Join(h.cwd, "b.txt")
	h.queue.Push(t, editScript("conv-s", a, "x\r\ny\r\nz\r\nw\r\n", "Changed a.txt."))
	h.queue.Push(t, editScript("conv-s", b, "b\nc\n", "Changed b.txt."))
	sum := h.delegate(api.DelegateRequest{Title: "eol", GateCommands: []string{gateCmd(t, "b.txt", "b")}})
	tk := h.waitStatus(sum.ID, StatusDone)
	if len(tk.Turns) != 2 || tk.Proof.AutoFixes != 1 {
		t.Fatalf("turns = %d, auto fixes = %d", len(tk.Turns), tk.Proof.AutoFixes)
	}
	first, fix := tk.Turns[0], tk.Turns[1]
	if first.Gate == nil || first.Gate.AutoFix != 2 || fix.By != ByGate {
		t.Fatalf("first gate = %+v, fix by %s", first.Gate, fix.By)
	}
	g := fix.Gate
	if g == nil || g.State != api.GateRed || g.AutoFix != 0 || len(g.Warnings) != 1 || len(g.NotRun) != 0 ||
		g.Steps[len(g.Steps)-1].State != api.StepGreen ||
		!strings.Contains(g.Warnings[0], "same line ending") {
		t.Fatalf("fix gate = %+v", g)
	}
	if a := tk.Attention; a == nil || a.Reason != ReasonHygiene {
		t.Fatalf("attention = %+v", tk.Attention)
	}
}

// A fix turn that repeats the line ending findings but also fails something else gets more fix turns; their
// prompts leave the repeated files out, which stay red for the lead.
func TestGateKeepsFixingOtherFailuresWithoutStuckStyle(t *testing.T) {
	h := newHarness(t)
	h.set(func(s *config.Settings) { s.GateMaxAutoFixes = 2 })
	h.initRepo(map[string]string{"a.txt": "x\ny\nz\n", "b.txt": "b\n"})
	h.agy("w", 1)
	a, b := filepath.Join(h.cwd, "a.txt"), filepath.Join(h.cwd, "b.txt")
	h.queue.Push(t, editScript("conv-o", a, "x\r\ny\r\nz\r\nw\r\n", "Changed a.txt."))
	h.queue.Push(t, editScript("conv-o", b, "still broken\n", "Changed b.txt."))
	h.queue.Push(t, editScript("conv-o", b, "fixed\n", "Fixed b.txt."))
	sum := h.delegate(api.DelegateRequest{Title: "eol and test", GateCommands: []string{gateCmd(t, "b.txt", "fixed")}})
	tk := h.waitStatus(sum.ID, StatusDone)
	if len(tk.Turns) != 3 || tk.Proof.AutoFixes != 2 {
		t.Fatalf("turns = %d, auto fixes = %d", len(tk.Turns), tk.Proof.AutoFixes)
	}
	const restore = "Restore the original line endings"
	if m := tk.Turns[1].Message; !strings.Contains(m, restore) || !strings.Contains(m, "a.txt (eol_changed)") {
		t.Fatalf("first fix prompt:\n%s", m)
	}
	if m := tk.Turns[2].Message; strings.Contains(m, restore) || strings.Contains(m, "a.txt") ||
		!strings.Contains(m, "gate-check b.txt fixed") {
		t.Fatalf("second fix prompt:\n%s", m)
	}
	g := tk.LastTurn().Gate
	if g == nil || g.State != api.GateRed || !hygieneOnly(g) {
		t.Fatalf("last gate = %+v", g)
	}
	if a := tk.Attention; a == nil || a.Reason != ReasonHygiene {
		t.Fatalf("attention = %+v", tk.Attention)
	}
}

func TestStuckStyleFindings(t *testing.T) {
	red := func(kind, path string) api.Finding {
		return api.Finding{Kind: kind, Severity: api.SeverityRed, Path: path}
	}
	hygiene := func(findings ...api.Finding) api.GateStep {
		return api.GateStep{Kind: api.StepHygiene, State: api.StepRed, Findings: findings}
	}
	gateOf := func(autoFix int, steps ...api.GateStep) api.GateResult {
		return api.GateResult{AutoFix: autoFix, Steps: steps}
	}
	eol := red(api.FindingEOLChanged, "a.txt")
	bom := red(api.FindingBOMChanged, "a.txt")
	junk := red(api.FindingJunkFile, "x.orig")
	test := api.GateStep{Kind: api.StepTask, State: api.StepRed, Cmd: "go test ./..."}
	foreign := api.GateStep{Kind: api.StepTask, State: api.StepRed, Cmd: "make lint", Foreign: true}
	started := gateOf(2, hygiene(eol, junk))
	tk := &Task{Turns: []*Turn{{N: 1, Gate: &started}, {N: 2, By: ByGate}}}
	for _, tc := range []struct {
		name         string
		res          api.GateResult
		stuck, alone bool
	}{
		{"same findings, junk cleared", gateOf(0, hygiene(eol)), true, true},
		{"same findings and a foreign failure", gateOf(0, hygiene(eol), foreign), true, true},
		{"same findings and a failing test", gateOf(0, hygiene(eol), test), true, false},
		{"same findings and a junk file", gateOf(0, hygiene(eol, junk)), true, false},
		{"another kind", gateOf(0, hygiene(bom)), false, false},
		{"one more", gateOf(0, hygiene(eol, bom)), false, false},
		{"cleared", gateOf(0, hygiene(junk)), false, false},
	} {
		stuck := stuckStyle(tk, 2, tc.res)
		if len(stuck) > 0 != tc.stuck || onlyFailures(tc.res, stuck) != tc.alone {
			t.Errorf("%s: stuck %v, alone %v", tc.name, stuck, onlyFailures(tc.res, stuck))
		}
	}
	// A turn no gate started (the lead's) is never stuck.
	if len(stuckStyle(tk, 1, gateOf(0, hygiene(eol)))) > 0 {
		t.Error("turn 1 is stuck")
	}

	// The prompt copy drops the stuck findings; a hygiene step left without red findings passes there, and the
	// stored result keeps them.
	res := gateOf(0, hygiene(eol), test)
	cp := withoutFindings(res, stuckStyle(tk, 2, res))
	if cp.Steps[0].State != api.StepGreen || len(cp.Steps[0].Findings) != 0 || cp.Steps[1].State != api.StepRed {
		t.Fatalf("prompt copy = %+v", cp.Steps)
	}
	if res.Steps[0].State != api.StepRed || len(res.Steps[0].Findings) != 1 {
		t.Fatalf("stored result changed: %+v", res.Steps)
	}
	res = gateOf(0, hygiene(eol, junk), test)
	stuck := map[[2]string]bool{{api.FindingEOLChanged, "a.txt"}: true}
	if cp := withoutFindings(res, stuck); cp.Steps[0].State != api.StepRed || len(cp.Steps[0].Findings) != 1 ||
		cp.Steps[0].Findings[0].Path != "x.orig" {
		t.Fatalf("prompt copy with junk = %+v", cp.Steps)
	}
}
