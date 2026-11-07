package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/gitx"
)

func TestAttentionAccountsAndRisk(t *testing.T) {
	set := config.Defaults()
	now := harnessEpoch
	none := accountHealth{known: true, total: 2, enabled: 2}
	signedOut := accountHealth{known: true, total: 1, enabled: 1, needsLogin: 1}
	for _, tc := range []struct {
		name   string
		t      *Task
		live   liveState
		acc    accountHealth
		reason string
	}{
		{"queued without a usable account", &Task{Status: StatusQueued, Backend: "agy"}, liveState{}, none,
			ReasonNoAccounts},
		{"queued, accounts unknown", &Task{Status: StatusQueued}, liveState{}, accountHealth{}, ""},
		{"waiting, every account signed out", &Task{Status: StatusWaitingQuota}, liveState{}, signedOut, ReasonAuth},
		{"running, account lost", &Task{Status: StatusRunning}, liveState{running: true, sawInit: true,
			lastEvent: now}, none, ReasonNoAccounts},
		{"running in a loop", &Task{Status: StatusRunning}, liveState{running: true, sawInit: true, lastEvent: now,
			steps: []loopStep{{key: "k"}, {key: "k"}, {key: "k"}, {key: "k"}}}, accountHealth{}, ReasonLooping},
		{"done with work at risk", &Task{Status: StatusDone, Turns: []*Turn{{N: 1, Response: "ok", EndedAt: now,
			WorkAtRisk: []api.RiskFile{{Path: "a"}}}}}, liveState{}, accountHealth{}, ReasonWorkAtRisk},
		{"done, restored", &Task{Status: StatusDone, Turns: []*Turn{{N: 1, Response: "ok", EndedAt: now,
			WorkAtRisk: []api.RiskFile{{Path: "a", Restored: true}}}}}, liveState{}, accountHealth{}, ReasonReview},
	} {
		got := computeAttention(tc.t, tc.live, tc.acc, now, set)
		reason := ""
		if got != nil {
			reason = got.Reason
		}
		if reason != tc.reason {
			t.Errorf("%s: reason %q, want %q", tc.name, reason, tc.reason)
		}
	}
}

// gitIn runs git in a repository the test created.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	p, err := gitx.NewBinary(nil, nil).Path()
	if err != nil {
		t.Skipf("no usable git: %v", err)
	}
	cmd := exec.CommandContext(t.Context(), p, args...)
	cmd.Dir = dir
	cmd.Env = gitx.Env(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// initRepo makes the harness project a repository with one commit of files.
func (h *harness) initRepo(files map[string]string) {
	h.t.Helper()
	gitIn(h.t, h.cwd, "init", "-q", "-b", "main")
	gitIn(h.t, h.cwd, "config", "user.name", "Test")
	gitIn(h.t, h.cwd, "config", "user.email", "test@example.invalid")
	gitIn(h.t, h.cwd, "config", "commit.gpgsign", "false")
	gitIn(h.t, h.cwd, "config", "core.autocrlf", "false")
	for name, text := range files {
		h.write(name, text)
	}
	gitIn(h.t, h.cwd, "add", ".")
	gitIn(h.t, h.cwd, "commit", "-q", "-m", "init")
}

func (h *harness) write(name, text string) {
	h.t.Helper()
	if err := writeText(filepath.Join(h.cwd, name), text); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) read(name string) string {
	h.t.Helper()
	data, err := os.ReadFile(filepath.Join(h.cwd, name))
	if err != nil {
		return "<missing>"
	}
	return string(data)
}

// fakeWrite is a script line that changes a file without an edit tool.
func fakeWrite(path, text string) string { return "#!write " + path + "\t" + strconv.Quote(text) }

func TestWorkAtRiskRestoreAndChecks(t *testing.T) {
	h := newHarness(t)
	h.initRepo(map[string]string{"a.txt": "one\ntwo\n", "c.txt": "c\n", "crlf.txt": "x\r\ny\r\nz\r\n"})
	// The user's uncommitted work: two lines in a.txt and a new notes.md.
	h.write("a.txt", "one\ntwo\nmine 1\nmine 2\n")
	h.write("notes.md", "draft\n")
	h.agy("w", 1)
	cwd := h.cwd
	h.queue.Push(t, script(agyInit("conv-r"),
		agyTool(1, "DONE", "git checkout -- a.txt; rm notes.md"),
		fakeWrite(filepath.Join(cwd, "a.txt"), "one\ntwo\n"),
		"#!remove "+filepath.Join(cwd, "notes.md"),
		fakeWrite(filepath.Join(cwd, "x.orig"), "leftover\n"),
		agyEdit(2, filepath.Join(cwd, "c.txt")),
		fakeWrite(filepath.Join(cwd, "c.txt"), "c\nmore\n"),
		fakeWrite(filepath.Join(cwd, "crlf.txt"), "x\r\ny\nz\nw\n"),
		agyResult("conv-r", "Changed c.txt. See https://docs.example.com/made-up-page for details.")))
	sum := h.delegate(api.DelegateRequest{Title: "risky"})
	tk := h.waitStatus(sum.ID, StatusDone)
	tu := tk.LastTurn()
	if tu.Snap == nil || !tu.Snap.Checked {
		t.Fatalf("snapshots: %+v", tu.Snap)
	}
	risk := map[string]api.RiskFile{}
	for _, r := range tu.WorkAtRisk {
		risk[r.Path] = r
	}
	if len(risk) != 2 || risk["a.txt"].Why != api.RiskOutsideTools || risk["a.txt"].LostLines != 2 ||
		!risk["notes.md"].Deleted {
		t.Fatalf("work at risk: %+v", tu.WorkAtRisk)
	}
	kinds := map[string]string{}
	for _, f := range tu.Hygiene {
		kinds[f.Path] = f.Kind
	}
	if kinds["x.orig"] != api.FindingJunkFile || kinds["crlf.txt"] != api.FindingEOLChanged || len(tu.Hygiene) != 2 {
		t.Fatalf("hygiene: %+v", tu.Hygiene)
	}
	if len(tu.Scope) != 1 || tu.Scope[0].Kind != api.FindingUnfetchedURL ||
		!strings.Contains(tu.Scope[0].Detail, "made-up-page") {
		t.Fatalf("scope: %+v", tu.Scope)
	}
	if !slices.Contains(tu.Changed, "c.txt") || !slices.Contains(tu.Preexisting, "a.txt") ||
		!slices.Equal(tu.Edited, []string{"c.txt"}) {
		t.Fatalf("changed %v, preexisting %v, edited %v", tu.Changed, tu.Preexisting, tu.Edited)
	}
	if a := tk.Attention; a == nil || a.Reason != ReasonHygiene {
		t.Fatalf("attention: %+v", tk.Attention)
	}
	if len(eventsOf(h.events(sum.ID), api.EventWorkAtRisk)) != 1 {
		t.Fatal("no work_at_risk event")
	}
	rep, err := h.s.Report(t.Context(), sum.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.WorkAtRisk == nil || !slices.Equal(rep.WorkAtRisk.Files, []string{"a.txt", "notes.md"}) ||
		!slices.Equal(rep.WorkAtRisk.RestoreArgv, []string{"seelegent", "restore", sum.ID, "--turn", "1",
			"--file=a.txt", "--file=notes.md"}) || len(rep.Hygiene) != 2 || len(rep.ScopeWarnings) != 1 {
		t.Fatalf("report: %+v", rep)
	}

	// The outside diff shows what no edit tool did; c.txt (edited by the tool) is not in it.
	diffs, err := h.s.OutsideDiff(t.Context(), sum.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, d := range diffs {
		paths = append(paths, d.Path)
	}
	if !slices.Contains(paths, "a.txt") || !slices.Contains(paths, "notes.md") || !slices.Contains(paths, "x.orig") ||
		slices.Contains(paths, "c.txt") {
		t.Fatalf("outside diff: %v", paths)
	}

	res, err := h.s.Restore(t.Context(), sum.ID, 1, api.RestoreRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Restored) != 2 || h.read("a.txt") != "one\ntwo\nmine 1\nmine 2\n" || h.read("notes.md") != "draft\n" {
		t.Fatalf("restore: %+v, a.txt %q", res, h.read("a.txt"))
	}
	tk = h.task(sum.ID)
	if atRisk(tk) != 0 {
		t.Fatalf("still at risk: %+v", tk.LastTurn().WorkAtRisk)
	}
	if rep, _ := h.s.Report(t.Context(), sum.ID, ""); rep.WorkAtRisk != nil {
		t.Fatalf("report after restore: %+v", rep.WorkAtRisk)
	}
	if _, err := h.s.Restore(t.Context(), sum.ID, 1, api.RestoreRequest{}); err == nil {
		t.Fatal("a second restore with nothing left must be refused")
	}
}

func TestWorkAtRiskSkipsOwnEditsAndClean(t *testing.T) {
	h := newHarness(t)
	h.initRepo(map[string]string{"a.txt": "one\n"})
	h.agy("w", 1)
	// The worker edits a clean file with its tool and creates a file it reports: nothing at risk, no findings.
	h.queue.Push(t, script(agyInit("conv-c"), agyEdit(1, filepath.Join(h.cwd, "a.txt")),
		fakeWrite(filepath.Join(h.cwd, "a.txt"), "two\n"),
		fakeWrite(filepath.Join(h.cwd, "b.txt"), "new\n"),
		agyResult("conv-c", "Rewrote a.txt and added b.txt.")))
	sum := h.delegate(api.DelegateRequest{Title: "clean"})
	tk := h.waitStatus(sum.ID, StatusDone)
	tu := tk.LastTurn()
	if len(tu.WorkAtRisk) != 0 || len(tu.Hygiene) != 0 || len(tu.Scope) != 0 {
		t.Fatalf("risk %+v hygiene %+v scope %+v", tu.WorkAtRisk, tu.Hygiene, tu.Scope)
	}
	if a := tk.Attention; a == nil || a.Reason != ReasonReview {
		t.Fatalf("attention: %+v", a)
	}
}

func TestFileOverlaps(t *testing.T) {
	h := newHarness(t)
	h.initRepo(map[string]string{"f.txt": "f\n"})
	h.agy("a", 2)
	h.agy("b", 1)
	f := filepath.Join(h.cwd, "f.txt")
	goA, goB := filepath.Join(h.dir, "go-a"), filepath.Join(h.dir, "go-b")
	h.queue.Push(t, script(agyInit("conv-a"), agyEdit(1, f), "#!wait-file "+goA, agyResult("conv-a", "A done.")))
	a := h.delegate(api.DelegateRequest{Title: "A"})
	h.waitFor("A's edit", func() bool { return len(h.s.live(a.ID).edited) == 1 })
	h.clk.Advance(time.Minute)
	h.queue.Push(t, script(agyInit("conv-b"), agyEdit(1, f), "#!wait-file "+goB, agyResult("conv-b", "B done.")))
	b := h.delegate(api.DelegateRequest{Title: "B"})
	h.waitFor("B's edit", func() bool { return len(h.s.live(b.ID).edited) == 1 })
	h.clk.Advance(time.Minute)
	h.s.forgetOverlaps()
	d, err := h.s.Get(t.Context(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.FileOverlaps) != 1 || d.FileOverlaps[0].File != "f.txt" || d.FileOverlaps[0].TaskID != b.ID ||
		d.FileOverlaps[0].Severity != api.OverlapActive {
		t.Fatalf("overlaps while both run: %+v", d.FileOverlaps)
	}
	for _, p := range []string{goA, goB} {
		if err := writeText(p, ""); err != nil {
			t.Fatal(err)
		}
	}
	h.waitStatus(a.ID, StatusDone)
	h.waitStatus(b.ID, StatusDone)
	h.s.forgetOverlaps()
	rep, err := h.s.Report(t.Context(), b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.FileOverlaps) != 1 || rep.FileOverlaps[0].TaskID != a.ID || rep.FileOverlaps[0].Severity != api.OverlapRecent {
		t.Fatalf("report overlaps: %+v", rep.FileOverlaps)
	}
	// Each task's change to f.txt is the other's: neither turn put the other's work at risk.
	if len(h.task(a.ID).LastTurn().WorkAtRisk) != 0 || len(h.task(b.ID).LastTurn().WorkAtRisk) != 0 {
		t.Fatal("overlapping tasks flagged each other")
	}
}

func TestSweepRemovesOldFolders(t *testing.T) {
	h := newHarness(t)
	h.initRepo(map[string]string{"a.txt": "a\n"})
	h.write("a.txt", "dirty\n")
	h.agy("w", 1)
	h.queue.Push(t, okScript("conv-s", "Nothing to do."))
	sum := h.delegate(api.DelegateRequest{Title: "old"})
	h.waitStatus(sum.ID, StatusDone)
	tasks := filepath.Join(h.dir, "tasks")
	orphan := filepath.Join(tasks, "t-gone")
	if err := mkdir(orphan); err != nil {
		t.Fatal(err)
	}
	h.s.sweep(t.Context())
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("the folder of a task that does not exist was kept")
	}
	if _, err := os.Stat(filepath.Join(tasks, sum.ID)); err != nil {
		t.Fatalf("a recent task lost its folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "snapshots", "tasks", sum.ID)); err != nil {
		t.Fatalf("a recent task lost its snapshots: %v", err)
	}
	h.clk.Advance(15 * 24 * time.Hour)
	h.s.sweep(t.Context())
	if _, err := os.Stat(filepath.Join(tasks, sum.ID)); !os.IsNotExist(err) {
		t.Fatal("an old finished task kept its folder")
	}
	if _, err := os.Stat(filepath.Join(h.dir, "snapshots", "tasks", sum.ID)); !os.IsNotExist(err) {
		t.Fatal("an old finished task kept its snapshots")
	}
}

func TestDetectLoop(t *testing.T) {
	st := func(keys ...string) []loopStep {
		var out []loopStep
		for _, k := range keys {
			out = append(out, loopStep{key: k, name: k, edit: strings.HasPrefix(k, "edit")})
		}
		return out
	}
	for _, tc := range []struct {
		name  string
		steps []loopStep
		want  bool
	}{
		{"same call four times", st("read", "read", "read", "read"), true},
		{"read between different edits (A9)", st("read", "edit1", "read", "edit2", "read", "edit3", "read"), false},
		{"read and list between edits", st("read", "ls", "edit1", "read", "ls", "edit2", "read", "ls", "edit3",
			"read"), false},
		{"the same pair over and over", st("read", "edit1", "read", "edit1", "read", "edit1", "read"), true},
		{"repeats with no progress", st("read", "ls", "read", "grep", "ls", "read", "x", "read"), true},
		{"three repeats", st("read", "read", "read"), false},
	} {
		if got, _ := detectLoop(tc.steps); got != tc.want {
			t.Errorf("%s: loop = %v", tc.name, got)
		}
	}
	failed := st("a", "b", "c", "d", "e")
	for i := range failed {
		failed[i].failed = true
	}
	if ok, detail := detectLoop(failed); !ok || !strings.Contains(detail, "5") {
		t.Errorf("failed calls: %v %q", ok, detail)
	}
}

func TestRestoreCommandQuoting(t *testing.T) {
	argv, line := restoreCommand("t1", 2, []string{"a b.txt", "-x", "it's;$(rm).txt"})
	if !slices.Equal(argv, []string{"seelegent", "restore", "t1", "--turn", "2", "--file=a b.txt", "--file=-x",
		"--file=it's;$(rm).txt"}) {
		t.Fatalf("argv = %q", argv)
	}
	if line != `seelegent restore t1 --turn 2 '--file=a b.txt' --file=-x '--file=it'\''s;$(rm).txt'` {
		t.Fatalf("line = %s", line)
	}
}

func TestScopeOutside(t *testing.T) {
	h := newHarness(t)
	other := t.TempDir()
	allowed := []string{rootKey(h.cwd)}
	for _, tc := range []struct {
		p    string
		want bool
	}{
		{filepath.Join(h.cwd, "a.txt"), false},
		{"rel/a.txt", false},
		{"/dev/null", false},
		{"~/x", false},
		{filepath.Join(other, "x"), true},
		{filepath.Join(h.cwd, "..", "escape.txt"), true},
	} {
		if _, got := h.s.outside(h.cwd, tc.p, allowed); got != tc.want {
			t.Errorf("outside(%q) = %v", tc.p, got)
		}
	}
}
