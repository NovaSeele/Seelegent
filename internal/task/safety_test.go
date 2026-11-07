package task

import (
	"encoding/json"
	"fmt"
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
	"github.com/NovaSeele/Seelegent/internal/event"
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
	// The transcript says what was put back.
	evs := h.events(sum.ID)
	restored := eventsOf(evs, api.EventRestored)
	if len(restored) != 1 || restored[0].Turn != 1 {
		t.Fatalf("restored events: %+v", restored)
	}
	var data api.RestoredData
	decode(t, restored[0], &data)
	slices.Sort(data.Files)
	if !slices.Equal(data.Files, []string{"a.txt", "notes.md"}) {
		t.Fatalf("restored data: %+v", data)
	}
	if rep, _ := h.s.Report(t.Context(), sum.ID, ""); rep.WorkAtRisk != nil {
		t.Fatalf("report after restore: %+v", rep.WorkAtRisk)
	}
	if _, err := h.s.Restore(t.Context(), sum.ID, 1, api.RestoreRequest{}); err == nil {
		t.Fatal("a second restore with nothing left must be refused")
	}
}

// A file someone changed after the turn ended is not overwritten unless forced; every restore names the backup
// snapshot that keeps what it replaced.
func TestRestoreSkipsFilesChangedAfterTheTurn(t *testing.T) {
	h := newHarness(t)
	h.initRepo(map[string]string{"a.txt": "one\n", "b.txt": "bee\n"})
	h.write("a.txt", "one\nmine\n")
	h.write("b.txt", "bee\nmine too\n")
	h.agy("w", 1)
	h.queue.Push(t, script(agyInit("conv-f"),
		agyTool(1, "DONE", "git checkout -- a.txt b.txt"),
		fakeWrite(filepath.Join(h.cwd, "a.txt"), "one\n"),
		fakeWrite(filepath.Join(h.cwd, "b.txt"), "bee\n"),
		agyResult("conv-f", "Reset the files.")))
	sum := h.delegate(api.DelegateRequest{Title: "reset"})
	tk := h.waitStatus(sum.ID, StatusDone)
	if len(tk.LastTurn().WorkAtRisk) != 2 {
		t.Fatalf("work at risk: %+v", tk.LastTurn().WorkAtRisk)
	}
	// The user goes on with a.txt after the turn.
	h.write("a.txt", "one\nnewer work\n")

	res, err := h.s.Restore(t.Context(), sum.ID, 1, api.RestoreRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Restored, []string{"b.txt"}) || len(res.Skipped) != 1 ||
		res.Skipped[0].Path != "a.txt" || res.Skipped[0].Code != api.RestoreChangedSinceTurn ||
		res.Skipped[0].Error != "changed since the turn ended" {
		t.Fatalf("restore: %+v", res)
	}
	if h.read("a.txt") != "one\nnewer work\n" || h.read("b.txt") != "bee\nmine too\n" {
		t.Fatalf("a.txt %q, b.txt %q", h.read("a.txt"), h.read("b.txt"))
	}
	if !strings.HasPrefix(res.Backup, "t1-restore-") {
		t.Fatalf("backup %q", res.Backup)
	}
	// The backup keeps what b.txt held before the restore.
	m, err := h.s.o.Snapshots.Load(sum.ID, 1, strings.TrimPrefix(res.Backup, "t1-"))
	if err != nil {
		t.Fatal(err)
	}
	b, ok := m.Find("b.txt")
	if !ok || len(m.Files) != 1 {
		t.Fatalf("backup files: %+v", m.Files)
	}
	if data, err := h.s.o.Snapshots.Content(b); err != nil || string(data) != "bee\n" {
		t.Fatalf("backup of b.txt: %q, %v", data, err)
	}
	var ev api.RestoredData
	decode(t, eventsOf(h.events(sum.ID), api.EventRestored)[0], &ev)
	if ev.Backup != res.Backup {
		t.Fatalf("event backup %q, result %q", ev.Backup, res.Backup)
	}

	// Forced, a.txt is put back too; the report's command never forces.
	h.clk.Advance(time.Second)
	res, err = h.s.Restore(t.Context(), sum.ID, 1, api.RestoreRequest{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Restored, []string{"a.txt"}) || len(res.Skipped) != 0 || h.read("a.txt") != "one\nmine\n" ||
		res.Backup == "" {
		t.Fatalf("forced restore: %+v, a.txt %q", res, h.read("a.txt"))
	}
	if atRisk(h.task(sum.ID)) != 0 {
		t.Fatal("files still at risk after the forced restore")
	}
}

// A file the task changed in an earlier turn is still checked: only the task's own lines count as committed, so
// lines the user added between the turns are reported when a later turn resets the file.
func TestWorkAtRiskKeepsTheUsersLinesInTheTasksFiles(t *testing.T) {
	h := newHarness(t)
	h.initRepo(map[string]string{"a.txt": "one\n", "b.txt": "bee\n"})
	h.agy("w", 1)
	a, b := filepath.Join(h.cwd, "a.txt"), filepath.Join(h.cwd, "b.txt")
	h.queue.Push(t, script(agyInit("conv-p"),
		agyEdit(1, a), fakeWrite(a, "one\ntask line\n"),
		agyEdit(2, b), fakeWrite(b, "bee\ntask bee\n"),
		agyResult("conv-p", "Added a line to each file.")))
	sum := h.delegate(api.DelegateRequest{Title: "two turns"})
	h.waitStatus(sum.ID, StatusDone)
	// Between the turns the user adds a line to a.txt; b.txt holds only the task's own line.
	h.write("a.txt", "one\ntask line\nuser line\n")
	h.queue.Push(t, script(agyInit("conv-p"),
		agyTool(1, "DONE", "git checkout -- a.txt b.txt"),
		fakeWrite(a, "one\n"), fakeWrite(b, "bee\n"),
		agyResult("conv-p", "Reset both files.")))
	if _, err := h.s.Followup(t.Context(), sum.ID, api.FollowupRequest{Message: "reset them"}); err != nil {
		t.Fatal(err)
	}
	h.waitFor("turn 2's checks", func() bool {
		tu := h.task(sum.ID).LastTurn()
		return tu.N == 2 && tu.Snap != nil && tu.Snap.Checked
	})
	risk := h.task(sum.ID).LastTurn().WorkAtRisk
	if len(risk) != 1 || risk[0].Path != "a.txt" || risk[0].Why != api.RiskOutsideTools || risk[0].LostLines != 1 {
		t.Fatalf("work at risk: %+v", risk)
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
		// Each run of four is judged on its own: progress before the last four repeats does not excuse them.
		{"progress only early on", st("read", "edit1", "read", "read", "ls", "read", "read"), true},
		{"progress in every run", st("read", "edit1", "read", "read", "edit2", "read", "read"), false},
		{"test runs between path-only edits marked by content (A1)", st("test", "edit\x00h1", "test", "edit\x00h2",
			"test", "edit\x00h3", "test"), false},
		{"test runs between path-only edits that change nothing", st("test", "edit\x00h1", "test", "edit\x00h1",
			"test", "edit\x00h1", "test"), true},
	} {
		if got, _ := detectLoop(tc.steps, 4); got != tc.want {
			t.Errorf("%s: loop = %v", tc.name, got)
		}
	}
	failed := st("a", "b", "c", "d", "e")
	for i := range failed {
		failed[i].failed = true
	}
	if ok, detail := detectLoop(failed, 4); !ok || !strings.Contains(detail, "5") {
		t.Errorf("failed calls: %v %q", ok, detail)
	}
}

// TestLoopPathOnlyEdits: an edit that reports only its file is keyed by the file's content, or by a number of its
// own when the file cannot be read, so a test run between real edits of one file is not a loop (bugs.md A1, old fix
// 492493d); an edit that leaves the file as it was still counts as a repeat.
func TestLoopPathOnlyEdits(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "handler.go")
	run := &event.Tool{Name: "run_command", State: event.ToolDone, Command: "go test ./..."}
	edit := func(path string) *event.Tool {
		in, _ := json.Marshal(map[string]any{"TargetFile": path})
		return &event.Tool{Name: "replace_file_content", State: event.ToolDone, Input: in}
	}
	tdd := func(contents []string, path string) []loopStep {
		var steps []loopStep
		for i, c := range contents {
			steps = addStep(steps, run, "", "")
			mark := ""
			if c != "" {
				if err := os.WriteFile(file, []byte(c), 0o600); err != nil {
					t.Fatal(err)
				}
				mark = fileMark(dir, path)
			} else {
				mark = fmt.Sprintf("#%d", i)
			}
			if pathOnlyEdit(edit(path)) == "" {
				t.Fatalf("%s is not a path-only edit", path)
			}
			steps = addStep(steps, edit(path), "", mark)
		}
		return addStep(steps, run, "", "")
	}
	if loop, detail := detectLoop(tdd([]string{"v1", "v2", "v3"}, file), 4); loop {
		t.Errorf("real edits (absolute path) flagged: %s", detail)
	}
	if loop, detail := detectLoop(tdd([]string{"v1", "v2", "v3"}, "handler.go"), 4); loop {
		t.Errorf("real edits (relative path) flagged: %s", detail)
	}
	if loop, detail := detectLoop(tdd([]string{"", "", ""}, filepath.Join(dir, "gone.go")), 4); loop {
		t.Errorf("unreadable edits flagged: %s", detail)
	}
	if loop, _ := detectLoop(tdd([]string{"same", "same", "same"}, file), 4); !loop {
		t.Error("edits that change nothing between the same test runs are a loop")
	}
	// Without a mark the old behaviour: the same key each time, a loop.
	var bare []loopStep
	for range 3 {
		bare = addStep(addStep(bare, run, "", ""), edit(file), "", "")
	}
	if loop, _ := detectLoop(addStep(bare, run, "", ""), 4); !loop {
		t.Error("unmarked path-only edits should look the same")
	}

	// Only successful edit tools whose parameters name nothing but the file are path-only.
	withContent, _ := json.Marshal(map[string]any{"TargetFile": file, "ReplacementContent": "x"})
	failed := edit(file)
	failed.State = event.ToolFailed
	for _, tl := range []*event.Tool{run, failed, {Name: "replace_file_content", Input: withContent},
		{Name: "view_file", State: event.ToolDone, Input: edit(file).Input}} {
		if got := pathOnlyEdit(tl); got != "" {
			t.Errorf("pathOnlyEdit(%s %s) = %q", tl.Name, tl.Input, got)
		}
	}
	if fileMark("", "rel.go") != "" || fileMark(dir, "missing.go") != "" || fileMark(dir, ".") != "" {
		t.Error("fileMark of an unreadable file")
	}
}

// TestLoopFalseAlarmPathOnlyEdits: four test runs between three path-only edits of the same file, as agy streams
// them, are not flagged as looping (the regression probe of bugs.md A1).
func TestLoopFalseAlarmPathOnlyEdits(t *testing.T) {
	for _, tc := range []struct {
		name string
		file func(h *harness) string
	}{
		{"file not readable", func(*harness) string { return filepath.Join(t.TempDir(), "gone", "handler.go") }},
		{"file in the project", func(h *harness) string { return filepath.Join(h.cwd, "handler.go") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.agy("only", 0)
			file := tc.file(h)
			test := func(i int) []string {
				return []string{agyTool(i, "CALLING", "go test ./..."), agyTool(i, "DONE", "go test ./...")}
			}
			// The worker goes on only once the daemon has seen its edit (as a live stream would be read).
			seen := func(i int) string { return filepath.Join(h.dir, fmt.Sprintf("seen-%d", i)) }
			lines := []string{agyInit("conv-l")}
			lines = append(lines, test(1)...)
			for i, v := range []string{"v1", "v2", "v3"} {
				lines = append(lines, "#!write "+file+"\t"+strconv.Quote(v), agyEdit(2*i+2, file),
					"#!wait-file "+seen(i))
				lines = append(lines, test(2*i+3)...)
			}
			lines = append(lines, "#!hang")
			h.queue.Push(t, script(lines...))
			sum := h.delegate(api.DelegateRequest{})
			for i := range 3 {
				h.waitFor("edit seen", func() bool { return len(h.s.live(sum.ID).steps) >= 2+2*i })
				if err := os.WriteFile(seen(i), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			h.waitFor("7 tool results", func() bool { return len(h.s.live(sum.ID).steps) >= 7 })
			live := h.s.live(sum.ID)
			if loop, detail := detectLoop(live.steps, h.config().LoopRepeat); loop {
				t.Errorf("a TDD loop with path-only edits is flagged as looping: %s", detail)
			}
			tk := h.task(sum.ID)
			if a := computeAttention(tk, live, accountHealth{}, h.clk.Now(), h.config()); a != nil &&
				a.Reason == ReasonLooping {
				t.Errorf("attention = %+v", a)
			}
			if _, _, err := h.s.Cancel(t.Context(), sum.ID, api.CancelRequest{WaitSeconds: 30}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestLoopRepeatSetting: the loopRepeat setting decides how many repeats are a loop (old bug B3: it was ignored and
// 4 was always used); a value outside 2..100 falls back to 4.
func TestLoopRepeatSetting(t *testing.T) {
	steps := func(n int) []loopStep {
		var out []loopStep
		for range n {
			out = append(out, loopStep{key: "go test", name: "run_command"})
		}
		return out
	}
	for _, tc := range []struct {
		repeat, calls int
		want          bool
	}{
		{4, 4, true}, {4, 3, false}, {8, 4, false}, {8, 7, false}, {8, 8, true}, {2, 2, true}, {2, 1, false},
		{0, 4, true}, {0, 3, false}, {1, 3, false}, {101, 4, true},
	} {
		if got, _ := detectLoop(steps(tc.calls), tc.repeat); got != tc.want {
			t.Errorf("loopRepeat %d, %d calls: loop = %v, want %v", tc.repeat, tc.calls, got, tc.want)
		}
	}

	// Through the attention rules, with the settings the engine reads.
	tk := &Task{ID: "t1", Status: StatusRunning, ConversationID: "c"}
	now := harnessEpoch
	live := liveState{running: true, sawInit: true, started: now, lastEvent: now, steps: steps(5)}
	set := config.Defaults()
	if a := computeAttention(tk, live, accountHealth{}, now, set); a == nil || a.Reason != ReasonLooping {
		t.Errorf("default loopRepeat: attention = %+v", a)
	}
	set.LoopRepeat = 8
	if a := computeAttention(tk, live, accountHealth{}, now, set); a != nil && a.Reason == ReasonLooping {
		t.Errorf("loopRepeat 8 flagged 5 repeats: %+v", a)
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
