package task

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/brief"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/gate"
	"github.com/NovaSeele/Seelegent/internal/snapshot"
)

// Field report 4: the lane and the gate phase (docs/gates.md). A plain lane entry covers its folder (F4a), a red
// lane never gets an automatic fix turn (F4b), the lane judges the net change since the instruction (F5) and only
// the files a worker touched (F6), a red lane never settles (F7), gate: false has no gate phase (F12) and a followup
// supersedes the gate phase (F13).

// laneRepo makes a repository with src/x/a.vue, src/xy.ts and b.txt.
func laneRepo(h *harness) {
	h.t.Helper()
	if err := mkdir(filepath.Join(h.cwd, "src", "x")); err != nil {
		h.t.Fatal(err)
	}
	h.initRepo(map[string]string{"src/x/a.vue": "a\n", "src/xy.ts": "xy\n", "b.txt": "b\n"})
}

// laneOf returns the lane step of the gate of turn n of task id.
func (h *harness) laneOf(tk *Task, n int) *api.GateStep {
	h.t.Helper()
	tu := tk.TurnN(n)
	if tu == nil || tu.Gate == nil {
		h.t.Fatalf("turn %d has no gate", n)
	}
	st := laneStep(*tu.Gate)
	if st == nil {
		h.t.Fatalf("gate of turn %d has no lane step: %+v", n, tu.Gate)
	}
	return st
}

// A worker edits a file in its lane folder and one next to it (src/xy.ts is not under src/x). The lane is red, no
// automatic fix turn runs although the budget allows two, and the task waits for the lead with the file named; the
// lead widens the lane with a followup and the gate goes green. The edit is reported in another spelling (upper case
// on Windows), as CLIs do.
func TestLaneOnlyRedStopsForLead(t *testing.T) {
	h := newHarness(t)
	h.set(func(s *config.Settings) { s.GateMaxAutoFixes = 2 })
	laneRepo(h)
	h.agy("w", 1)
	in := filepath.Join(h.cwd, "src", "x", "a.vue")
	out := filepath.Join(h.cwd, "src", "xy.ts")
	reported := out
	if runtime.GOOS == "windows" {
		reported = strings.ToUpper(out)
	}
	h.queue.Push(t, script(agyInit("conv-l"), agyEdit(1, in), fakeWrite(in, "b\n"), agyEdit(2, reported),
		fakeWrite(out, "changed\n"), agyResult("conv-l", "Changed both.")))
	sum := h.delegate(api.DelegateRequest{Title: "lane only", Lane: []string{"src/x"}})
	tk := h.waitStatus(sum.ID, StatusDone)
	if len(tk.Turns) != 1 || tk.Proof.AutoFixes != 0 {
		t.Fatalf("turns = %d, auto fixes = %d", len(tk.Turns), tk.Proof.AutoFixes)
	}
	g := tk.LastTurn().Gate
	if st := h.laneOf(tk, 1); g.State != api.GateRed || st.State != api.StepRed ||
		!slices.Equal(st.Files, []string{"src/xy.ts"}) || len(st.Unseen) != 0 {
		t.Fatalf("gate = %+v", g)
	}
	a := tk.Attention
	if a == nil || a.Reason != ReasonGateRed || a.Who != WhoLead || !api.NeedsLead(a.State) ||
		!strings.Contains(a.Detail, "src/xy.ts") {
		t.Errorf("attention = %+v", a)
	}
	rep, err := h.s.Report(t.Context(), sum.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Lane == nil || !slices.Equal(rep.Lane.OutOfLane, []string{"src/xy.ts"}) || rep.Gate == nil ||
		!slices.ContainsFunc(rep.Gate.Warnings, func(w string) bool {
			return strings.Contains(w, "src/xy.ts") && strings.Contains(w, "No automatic fix turn for the lane")
		}) {
		t.Errorf("report lane = %+v gate = %+v", rep.Lane, rep.Gate)
	}
	// The lead widens the lane: the next gate judges the new instruction's change against it.
	h.queue.Push(t, okScript("conv-l", "Nothing else to change."))
	if _, err := h.s.Followup(t.Context(), sum.ID, api.FollowupRequest{Message: "Keep src/xy.ts.",
		Lane: []string{"src/x", "src/xy.ts"}}); err != nil {
		t.Fatal(err)
	}
	tk = h.waitStatus(sum.ID, StatusDone)
	if len(tk.Turns) != 2 || !slices.Equal(tk.Proof.Lane.Paths, []string{"src/x/", "src/xy.ts"}) {
		t.Fatalf("turns = %d lane = %+v", len(tk.Turns), tk.Proof.Lane)
	}
	if g := tk.LastTurn().Gate; g != nil && g.State != api.GateGreen {
		t.Errorf("gate after widening = %+v", g)
	}
	if a := tk.Attention; a != nil && a.Reason == ReasonGateRed {
		t.Errorf("attention after widening = %+v", a)
	}
}

// laneFixCase is a lane plus command red gate whose fix turn takes the out-of-lane change back.
type laneFixCase struct {
	name         string
	first, fixed func(h *harness) string // the scripts of the two turns
}

// The lane is red together with a gate command: the fix turn works on the command, and its prompt names the lane
// files as information with the no-delete line. The fix turn happens to take the out-of-lane change back (F5): a
// file created and deleted again, or changed and restored, is no change, and the gate after it is green.
func TestLaneNetChange(t *testing.T) {
	a := func(h *harness) string { return filepath.Join(h.cwd, "src", "x", "a.vue") }
	cases := []laneFixCase{
		{"created then deleted",
			func(h *harness) string {
				o := filepath.Join(h.cwd, "notes.md")
				return script(agyInit("c"), agyEdit(1, a(h)), fakeWrite(a(h), "broken\n"), agyEdit(2, o),
					fakeWrite(o, "notes\n"), agyResult("c", "Changed."))
			},
			func(h *harness) string {
				return script(agyInit("c"), agyEdit(1, a(h)), fakeWrite(a(h), "fixed\n"),
					"#!remove "+filepath.Join(h.cwd, "notes.md"), agyResult("c", "Fixed."))
			}},
		{"changed then restored",
			func(h *harness) string {
				b := filepath.Join(h.cwd, "b.txt")
				return script(agyInit("c"), agyEdit(1, a(h)), fakeWrite(a(h), "broken\n"), agyEdit(2, b),
					fakeWrite(b, "changed\n"), agyResult("c", "Changed."))
			},
			func(h *harness) string {
				b := filepath.Join(h.cwd, "b.txt")
				return script(agyInit("c"), agyEdit(1, a(h)), fakeWrite(a(h), "fixed\n"), agyEdit(2, b),
					fakeWrite(b, "b\n"), agyResult("c", "Fixed."))
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.set(func(s *config.Settings) { s.GateMaxAutoFixes = 2 })
			laneRepo(h)
			h.agy("w", 1)
			h.queue.Push(t, c.first(h))
			h.queue.Push(t, c.fixed(h))
			sum := h.delegate(api.DelegateRequest{Title: "net", Lane: []string{"src/x"},
				GateCommands: []string{gateCmd(t, "src/x/a.vue", "fixed")}})
			tk := h.waitStatus(sum.ID, StatusDone)
			if len(tk.Turns) != 2 || tk.Turns[1].By != ByGate {
				t.Fatalf("turns = %d", len(tk.Turns))
			}
			first := h.laneOf(tk, 1)
			if first.State != api.StepRed || len(first.Files) != 1 {
				t.Fatalf("first lane = %+v", first)
			}
			msg := tk.Turns[1].Message
			for _, want := range []string{gate.NoDeleteLine, brief.DataRule("", tk.Cwd, ""), "your lane is src/x/",
				"outside it: " + first.Files[0], "does not contain"} {
				if !strings.Contains(msg, want) {
					t.Errorf("fix prompt misses %q:\n%s", want, msg)
				}
			}
			if tk.Turns[1].Snap == nil {
				t.Error("the automatic fix turn has no snapshots")
			}
			if g := tk.Turns[1].Gate; g == nil || g.State != api.GateGreen || h.laneOf(tk, 2).State != api.StepGreen {
				t.Errorf("gate after the fix = %+v", g)
			}
		})
	}
}

// netLane falls back to the union whenever the snapshots cannot tell: a truncated start or end, HEAD moved, or a
// snapshot missing.
func TestNetLaneFailsClosed(t *testing.T) {
	h := newHarness(t)
	tk := &Task{ID: "net1", Turns: []*Turn{{N: 1, By: ByLead, Snap: &TurnSnap{}}, {N: 2, By: ByGate, Snap: &TurnSnap{}}}}
	save := func(n int, kind string, m snapshot.Manifest) {
		t.Helper()
		if err := h.s.o.Snapshots.Save(tk.ID, n, kind, m); err != nil {
			t.Fatal(err)
		}
	}
	start := snapshot.Manifest{Version: 1, Head: "h1", Files: []snapshot.File{
		{Path: "b.txt", Status: api.GitModified, Hash: "b1", Size: 2},
		{Path: "d.txt", Status: api.GitModified, Hash: "d1", Size: 2},
	}}
	// d.txt changed: on Windows and macOS D.TXT is the same file with d2; on Linux it is another file, and d.txt,
	// dirty at the start and gone from the end, went back to HEAD, which is a change too.
	end := snapshot.Manifest{Version: 1, Head: "h1", Files: []snapshot.File{
		{Path: "b.txt", Status: api.GitModified, Hash: "b1", Size: 2},  // restored to what it was
		{Path: "c.txt", Status: api.GitUntracked, Hash: "c1", Size: 2}, // new
		{Path: "D.TXT", Status: api.GitModified, Hash: "d2", Size: 2},
	}}
	save(1, snapshot.KindStart, start)
	save(2, snapshot.KindEnd, end)
	union := []string{"a.txt", "b.txt", "c.txt", "d.txt"} // a.txt: created and deleted again
	if got := h.s.netLane(tk, 2, union, nil); !slices.Equal(got, []string{"c.txt", "d.txt"}) {
		t.Errorf("net = %v, want [c.txt d.txt]", got)
	}
	// The spelling matters only where the file system ignores letter case (snapshot.Key): with d.txt unchanged and a
	// new D.TXT next to it, Linux sees d.txt as it was, Windows and macOS find D.TXT (listed first) as d.txt.
	spelled := snapshot.Manifest{Version: 1, Head: "h1", Files: []snapshot.File{
		{Path: "b.txt", Status: api.GitModified, Hash: "b1", Size: 2},
		{Path: "c.txt", Status: api.GitUntracked, Hash: "c1", Size: 2},
		{Path: "D.TXT", Status: api.GitUntracked, Hash: "d2", Size: 2},
		{Path: "d.txt", Status: api.GitModified, Hash: "d1", Size: 2},
	}}
	save(2, snapshot.KindEnd, spelled)
	want := []string{"c.txt"}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		want = []string{"c.txt", "d.txt"}
	}
	if got := h.s.netLane(tk, 2, union, nil); !slices.Equal(got, want) {
		t.Errorf("net with D.TXT next to d.txt = %v, want %v", got, want)
	}
	for name, change := range map[string]func(s, e *snapshot.Manifest){
		"truncated end":   func(_, e *snapshot.Manifest) { e.Truncated = true },
		"truncated start": func(s, _ *snapshot.Manifest) { s.Truncated = true },
		"head moved":      func(_, e *snapshot.Manifest) { e.Head = "h2" },
	} {
		s, e := start, end
		change(&s, &e)
		save(1, snapshot.KindStart, s)
		save(2, snapshot.KindEnd, e)
		if got := h.s.netLane(tk, 2, union, nil); !slices.Equal(got, union) {
			t.Errorf("%s: net = %v, want the union", name, got)
		}
	}
	tk.Turns[0].Snap = nil
	if got := h.s.netLane(tk, 2, union, nil); !slices.Equal(got, union) {
		t.Errorf("no start snapshot: net = %v", got)
	}
}

// The field case: while the worker's turn runs, someone else (the lead, the user) writes a file outside the lane
// directly, with no edit event and no command of the worker. The lane is a warning: no fix turn, no stop for the
// lead, and the file is still named in the report as changed outside the lane, not by this worker.
func TestLaneOutsideWriteIsWarning(t *testing.T) {
	h := newHarness(t)
	h.set(func(s *config.Settings) { s.GateMaxAutoFixes = 2 })
	laneRepo(h)
	h.agy("w", 1)
	in := filepath.Join(h.cwd, "src", "x", "a.vue")
	h.queue.Push(t, script(agyInit("conv-o"), agyEdit(1, in), fakeWrite(in, "b\n"),
		fakeWrite(filepath.Join(h.cwd, "src", "safety.ts"), "export const x = 1\n"), agyResult("conv-o", "Changed a.vue.")))
	sum := h.delegate(api.DelegateRequest{Title: "outside write", Lane: []string{"src/x"}})
	tk := h.waitStatus(sum.ID, StatusDone)
	if len(tk.Turns) != 1 || tk.Proof.AutoFixes != 0 {
		t.Fatalf("turns = %d", len(tk.Turns))
	}
	st := h.laneOf(tk, 1)
	if g := tk.LastTurn().Gate; g.State != api.GateGreen || st.State != api.StepWarn || len(st.Files) != 0 ||
		!slices.Equal(st.Unseen, []string{"src/safety.ts"}) {
		t.Fatalf("gate = %+v", g)
	}
	if a := tk.Attention; a != nil && (a.Reason == ReasonGateRed || api.NeedsLead(a.State)) {
		t.Errorf("attention = %+v", a)
	}
	rep, err := h.s.Report(t.Context(), sum.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	wantWarn := "outside the lane, " + gate.UnseenNote + ": src/safety.ts"
	if rep.Lane == nil || len(rep.Lane.OutOfLane) != 0 || !slices.Contains(rep.Lane.Warnings, wantWarn) {
		t.Errorf("report lane = %+v", rep.Lane)
	}
	if rep.Gate == nil || !slices.Contains(rep.Gate.Warnings, wantWarn) {
		t.Errorf("report gate = %+v", rep.Gate)
	}
}

// The opposite case: a file the worker wrote with a shell command Seelegent read (echo > x) counts against the lane.
func TestLaneCommandWriteIsRed(t *testing.T) {
	h := newHarness(t)
	laneRepo(h)
	h.agy("w", 1)
	h.queue.Push(t, script(agyInit("conv-e"), agyTool(1, "DONE", "echo hi > notes.txt"),
		fakeWrite(filepath.Join(h.cwd, "notes.txt"), "hi\n"), agyResult("conv-e", "Wrote notes.")))
	sum := h.delegate(api.DelegateRequest{Title: "command write", Lane: []string{"src/x"}})
	tk := h.waitStatus(sum.ID, StatusDone)
	if st := h.laneOf(tk, 1); st.State != api.StepRed || !slices.Equal(st.Files, []string{"notes.txt"}) {
		t.Fatalf("lane = %+v", st)
	}
	if a := tk.Attention; a == nil || a.Reason != ReasonGateRed {
		t.Errorf("attention = %+v", a)
	}
}

// workerWrites reads the paths of PowerShell commands against the task's folder, a removed folder counting for
// every file under it (the field run: Remove-Item -Path src/components/lighting -Recurse), follows a cd of the same
// line, and lists a command that writes where it cannot read without claiming a file.
func TestLaneTouchCommands(t *testing.T) {
	root := t.TempDir()
	if err := mkdir(filepath.Join(root, "src", "components", "lighting")); err != nil {
		t.Fatal(err)
	}
	w := newWorkerWrites(root, root, nil, []string{
		"Remove-Item -Path src/components/lighting -Recurse -Force",
		`Set-Content -LiteralPath 'src\store.ts' -Value x`,
		"cd src && rm old/*.vue",
		"echo x > ../outside.txt",
		"Set-Location lib; Remove-Item gen -Recurse",
		"cd app && npm init -y > init.log",
	})
	for _, f := range []string{"src/components/lighting/Lamp.vue", "src/components/lighting/deep/a.ts",
		"src/store.ts", "src/old/b.vue", "lib/gen/x.ts", "app/init.log"} {
		if !w.wrote(f) {
			t.Errorf("%s not touched (%+v)", f, w)
		}
	}
	for _, f := range []string{"src/components/light.ts", "src/old/b.ts", "outside.txt", "src/store.tsx",
		"old/b.vue", "gen/x.ts", "init.log"} {
		if w.wrote(f) {
			t.Errorf("%s touched (%+v)", f, w)
		}
	}
	if runtime.GOOS == "windows" && !w.wrote("SRC/Store.ts") {
		t.Error("letter case matters on Windows")
	}
	if len(w.unknown) != 0 {
		t.Errorf("unknown = %q", w.unknown)
	}
	// Writes whose place the line does not spell out claim nothing and are listed; the temporary folder is neither.
	unknown := []string{"Remove-Item -Path $env:OUT -Recurse", "cp -r ../tpl/* .", "rm -rf ."}
	w = newWorkerWrites(root, root, nil, append(slices.Clone(unknown), `echo x > $env:TEMP\log.txt`,
		"rm -f $TMPDIR/x", "cd $(mktemp -d) && touch x"))
	if !slices.Equal(w.unknown, unknown) || w.wrote("src/store.ts") {
		t.Errorf("unknown = %q, keys %q", w.unknown, w.keys)
	}
}

// A worker's command that wrote where Seelegent could not read makes the lane fail closed: a file outside the lane
// that no edit or readable command explains counts against it, and the lane step says why.
func TestLaneUnknownWriteIsRed(t *testing.T) {
	h := newHarness(t)
	laneRepo(h)
	h.agy("w", 1)
	in := filepath.Join(h.cwd, "src", "x", "a.vue")
	h.queue.Push(t, script(agyInit("conv-u"), agyEdit(1, in), fakeWrite(in, "b\n"),
		agyTool(2, "DONE", "Remove-Item -Path $env:OUT -Recurse"),
		fakeWrite(filepath.Join(h.cwd, "b.txt"), "gone\n"), agyResult("conv-u", "Changed a.vue.")))
	sum := h.delegate(api.DelegateRequest{Title: "unknown write", Lane: []string{"src/x"}})
	tk := h.waitStatus(sum.ID, StatusDone)
	st := h.laneOf(tk, 1)
	if st.State != api.StepRed || !slices.Equal(st.Files, []string{"b.txt"}) || len(st.Unseen) != 0 ||
		!strings.Contains(st.Tail, gate.UnknownNote) || !strings.Contains(st.Tail, "$env:OUT") {
		t.Fatalf("lane = %+v", st)
	}
}

// The edit tools' files are resolved like the repository root on both sides: an absolute path in another spelling
// (forward or back slashes, letter case, an 8.3 short name, a junction or symbolic link) still names the file.
func TestLaneTouchEditSpellings(t *testing.T) {
	h := newHarness(t)
	laneRepo(h)
	tk := &Task{Cwd: h.cwd, RepoRoot: h.cwd}
	abs := filepath.Join(h.cwd, "src", "xy.ts")
	spellings := map[string]string{"absolute": abs, "relative": filepath.Join("src", "xy.ts"),
		"slashes": filepath.ToSlash(abs)}
	for i, s := range fsutil.Spellings(abs) {
		spellings[fmt.Sprintf("spelling %d", i)] = s // canonical and 8.3 forms
	}
	link := filepath.Join(h.dir, "linked")
	var err error
	if runtime.GOOS == "windows" {
		spellings["backslashes"] = strings.ReplaceAll(abs, "/", `\`)
		spellings["case"] = strings.ToUpper(abs)
		spellings["git bash"] = "/" + strings.ToLower(abs[:1]) + filepath.ToSlash(abs[2:])
		var out []byte
		if out, err = exec.CommandContext(t.Context(), "cmd.exe", "/d", "/c", "mklink", "/J", link, h.cwd).
			CombinedOutput(); err != nil {
			err = fmt.Errorf("%w: %s", err, out)
		}
	} else {
		err = os.Symlink(h.cwd, link)
	}
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	spellings["link"] = filepath.Join(link, "src", "xy.ts")
	for name, p := range spellings {
		tu := &Turn{N: 1, By: ByLead, Status: TurnOK,
			Attempts: []*Attempt{{N: 1, Outcome: &backend.Outcome{FilesEdited: []string{p}}}}}
		files := turnFiles(tk, tu, liveState{})
		w := newWorkerWrites(tk.Cwd, tk.RepoRoot, files, nil)
		if !w.wrote("src/xy.ts") || w.wrote("src/x/a.vue") {
			t.Errorf("%s (%s): edited %v", name, p, files)
		}
	}
}

// settled reports whether the gate of task id waited for the other workers.
func (h *harness) settled(id string) bool {
	h.t.Helper()
	for _, e := range eventsOf(h.events(id), api.EventGate) {
		var d api.GateEventData
		decode(h.t, e, &d)
		if d.Phase == api.GateEventSettling {
			return true
		}
	}
	return false
}

// A red lane never waits for the other workers of the repository (F7): their work cannot change it. A red command
// still does.
func TestLaneRedNeverSettles(t *testing.T) {
	h := newHarness(t)
	laneRepo(h)
	h.agy("w1", 1)
	h.agy("w2", 1)
	h.queue.Push(t, hangScript("conv-b"))
	other := h.delegate(api.DelegateRequest{Title: "other"})
	h.runningWorker(other.ID)
	h.queue.Push(t, editScript("conv-l", filepath.Join(h.cwd, "src", "xy.ts"), "changed\n", "Changed."))
	lane := h.delegate(api.DelegateRequest{Title: "lane", Lane: []string{"src/x"}})
	tk := h.waitStatus(lane.ID, StatusDone)
	if st := h.laneOf(tk, 1); st.State != api.StepRed {
		t.Fatalf("lane = %+v", st)
	}
	if h.settled(lane.ID) {
		t.Error("a gate red only on its lane waited for the other workers")
	}
	h.queue.Push(t, editScript("conv-c", filepath.Join(h.cwd, "b.txt"), "broken\n", "Changed."))
	cmd := h.delegate(api.DelegateRequest{Title: "command", GateCommands: []string{gateCmd(t, "b.txt", "fixed")}})
	h.waitStatus(cmd.ID, StatusDone)
	if !h.settled(cmd.ID) {
		t.Error("a red gate command did not wait for the other workers")
	}
}

// gate: false has no gate phase (F12): no gate, no fix turn, no settling. The lane is still checked after the turn,
// with the same rules, as a warning in the turn's scope findings and the report.
func TestGateOffLaneIsInformation(t *testing.T) {
	h := newHarness(t)
	h.set(func(s *config.Settings) { s.GateMaxAutoFixes = 2 })
	laneRepo(h)
	h.agy("w", 1)
	h.queue.Push(t, editScript("conv-g", filepath.Join(h.cwd, "src", "xy.ts"), "changed\n", "Changed."))
	no := false
	sum := h.delegate(api.DelegateRequest{Title: "gate off", Lane: []string{"src/x"}, Gate: &no})
	tk := h.waitStatus(sum.ID, StatusDone)
	tu := tk.LastTurn()
	if len(tk.Turns) != 1 || tu.Gate != nil || tk.Proof.AutoFixes != 0 {
		t.Fatalf("turns = %d gate = %+v", len(tk.Turns), tu.Gate)
	}
	if n := len(eventsOf(h.events(sum.ID), api.EventGate)); n != 0 {
		t.Errorf("%d gate events", n)
	}
	i := slices.IndexFunc(tu.Scope, func(f api.Finding) bool { return f.Kind == api.FindingOutsideLane })
	if i < 0 || tu.Scope[i].Path != "src/xy.ts" || tu.Scope[i].Severity != api.SeverityWarn {
		t.Errorf("scope = %+v", tu.Scope)
	}
	if a := tk.Attention; a != nil && a.Reason == ReasonGateRed {
		t.Errorf("attention = %+v", a)
	}
	rep, err := h.s.Report(t.Context(), sum.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Gate != nil || rep.Lane == nil || !slices.Equal(rep.Lane.OutOfLane, []string{"src/xy.ts"}) ||
		!slices.ContainsFunc(rep.Lane.Warnings, func(w string) bool { return strings.Contains(w, "gate off") }) {
		t.Errorf("report gate = %+v lane = %+v", rep.Gate, rep.Lane)
	}
}

// A followup while the gate settles (the worker's turn has ended) supersedes the gate: it stops, is recorded as
// skipped with the reason, and the new turn runs (F13). A followup while the worker's turn runs is refused, saying so.
func TestFollowupSupersedesGate(t *testing.T) {
	h := newHarness(t)
	h.set(func(s *config.Settings) { s.GateSettleMinutes = 5; s.GateSettlePollMs = 100 })
	h.initRepo(map[string]string{"a.txt": "a\n"})
	h.agy("w1", 1)
	h.agy("w2", 1)
	h.queue.Push(t, hangScript("conv-b"))
	other := h.delegate(api.DelegateRequest{Title: "other"})
	h.runningWorker(other.ID)
	_, err := h.s.Followup(t.Context(), other.ID, api.FollowupRequest{Message: "More."})
	if !errors.Is(err, ErrBadState) || !strings.Contains(err.Error(), "the worker's turn is still running") {
		t.Errorf("followup during the turn = %v", err)
	}
	a := filepath.Join(h.cwd, "a.txt")
	h.queue.Push(t, editScript("conv-s", a, "broken\n", "Changed."))
	h.queue.Push(t, editScript("conv-s", a, "fixed\n", "Fixed."))
	sum := h.delegate(api.DelegateRequest{Title: "settling", GateCommands: []string{gateCmd(t, "a.txt", "fixed")}})
	h.waitFor("the gate to settle", func() bool {
		ph := h.task(sum.ID).GatePhase
		return ph != nil && ph.State == api.GatePhaseSettling
	})
	if _, err := h.s.Followup(t.Context(), sum.ID, api.FollowupRequest{Message: "Write fixed into a.txt."}); err != nil {
		t.Fatal(err)
	}
	tk := h.waitStatus(sum.ID, StatusDone)
	if len(tk.Turns) != 2 || tk.Turns[1].By != ByLead {
		t.Fatalf("turns = %d", len(tk.Turns))
	}
	if g := tk.Turns[0].Gate; g == nil || g.State != api.GateSkipped || !slices.Contains(g.Warnings, supersededNote) {
		t.Errorf("superseded gate = %+v", g)
	}
	if g := tk.Turns[1].Gate; g == nil || g.State != api.GateGreen {
		t.Errorf("gate of the new turn = %+v", g)
	}
}

// After the lead rejects a task with files outside its lane, the followup's prompt tells the worker not to delete or
// move files and gives the lead's reasons (F4).
func TestRejectedLaneFollowupKeepsFiles(t *testing.T) {
	h := newHarness(t)
	laneRepo(h)
	h.agy("w", 1)
	h.queue.Push(t, editScript("conv-r", filepath.Join(h.cwd, "src", "xy.ts"), "changed\n", "Changed."))
	sum := h.delegate(api.DelegateRequest{Title: "rejected", Lane: []string{"src/x"}})
	h.waitStatus(sum.ID, StatusDone)
	if _, err := h.s.Review(t.Context(), sum.ID, api.ReviewRequest{Verdict: api.VerdictRejected,
		Notes: "a.vue still shows the old label"}); err != nil {
		t.Fatal(err)
	}
	h.queue.Push(t, okScript("conv-r", "Done."))
	if _, err := h.s.Followup(t.Context(), sum.ID, api.FollowupRequest{Message: "Fix the label."}); err != nil {
		t.Fatal(err)
	}
	tk := h.waitStatus(sum.ID, StatusDone)
	msg := tk.Turns[1].Message
	for _, want := range []string{"Do not delete or move files", "the lead rejected for: a.vue still shows the old label",
		"Fix the label."} {
		if !strings.Contains(msg, want) {
			t.Errorf("followup message misses %q:\n%s", want, msg)
		}
	}
	// Without files outside the lane a reject adds nothing.
	tk.Review = &api.Review{Verdict: api.VerdictRejected, Notes: "x"}
	tk.Turns[1].Gate = &api.GateResult{State: api.GateGreen, Steps: []api.GateStep{{Kind: api.StepLane, State: api.StepGreen}}}
	if n := rejectNote(tk); n != "" {
		t.Errorf("note without lane files = %q", n)
	}
}

// fakeMtime is a script line that sets a file's modification time: a write at that time, on the harness clock.
func fakeMtime(path string, at time.Time) string {
	return "#!mtime " + path + "\t" + strconv.FormatInt(at.UnixNano(), 10)
}

// A worker's script writes a file outside the lane that no word of its command names (python -c, as Gemini does
// for bulk edits). The file changed while the command ran, by its call and result events: red (review).
func TestLaneScriptWriteIsRed(t *testing.T) {
	h := newHarness(t)
	laneRepo(h)
	h.agy("w", 1)
	cmd := `python -c "open('docs/x.md','w').write('x')"`
	out := filepath.Join(h.cwd, "docs", "x.md")
	h.queue.Push(t, script(agyInit("conv-p"), agyTool(1, "CALLING", cmd), fakeWrite(out, "x"),
		fakeMtime(out, h.clk.Now().Add(time.Second)), agyTool(1, "DONE", cmd), agyResult("conv-p", "Wrote the docs.")))
	sum := h.delegate(api.DelegateRequest{Title: "script write", Lane: []string{"src/"}})
	tk := h.waitStatus(sum.ID, StatusDone)
	st := h.laneOf(tk, 1)
	if st.State != api.StepRed || !slices.Equal(st.Files, []string{"docs/x.md"}) ||
		len(st.Unseen) != 0 || strings.Contains(st.Tail, gate.UnknownNote) {
		t.Fatalf("lane = %+v", st)
	}
	// The lead sees why: a red lane has no fix turn (review G1).
	if want := "Of these, " + gate.TimedNote + ":\ndocs/x.md: changed while `" + cmd + "` ran"; !strings.Contains(st.Tail,
		want) {
		t.Errorf("tail = %q, want %q", st.Tail, want)
	}
}

// With the gate off the lane is a scope finding; a file counted by time says which command it changed during, and a
// file a code runner's turn deleted says which runner ran (review G1).
func TestGateOffLaneSaysWhy(t *testing.T) {
	h := newHarness(t)
	laneRepo(h)
	h.agy("w", 1)
	cmd := `node gen.js`
	out := filepath.Join(h.cwd, "docs", "g.md")
	h.queue.Push(t, script(agyInit("conv-w"), agyTool(1, "CALLING", cmd), fakeWrite(out, "g\n"),
		fakeMtime(out, h.clk.Now()), "#!remove "+filepath.Join(h.cwd, "b.txt"), agyTool(1, "DONE", cmd),
		agyResult("conv-w", "Generated.")))
	no := false
	sum := h.delegate(api.DelegateRequest{Title: "gate off why", Lane: []string{"src/"}, Gate: &no})
	tk := h.waitStatus(sum.ID, StatusDone)
	details := map[string]string{}
	for _, f := range tk.LastTurn().Scope {
		if f.Kind == api.FindingOutsideLane {
			details[f.Path] = f.Detail
		}
	}
	if d := details["docs/g.md"]; d != "outside the lane, changed while `node gen.js` ran" {
		t.Errorf("docs/g.md: %q (scope %+v)", d, tk.LastTurn().Scope)
	}
	if d := details["b.txt"]; d != "outside the lane, deleted, and the worker ran `node gen.js`" {
		t.Errorf("b.txt: %q (scope %+v)", d, tk.LastTurn().Scope)
	}
}

// The daemon stamps the call event when it reads it, up to a second (and more on a busy machine) after the command
// started: a file the script wrote 800 ms before the call's time still counts (review N1).
func TestLaneWriteBeforeCallEventIsRed(t *testing.T) {
	h := newHarness(t)
	laneRepo(h)
	h.agy("w", 1)
	cmd := `node gen.js`
	out := filepath.Join(h.cwd, "docs", "early.md")
	h.queue.Push(t, script(agyInit("conv-e"), fakeWrite(out, "e\n"),
		fakeMtime(out, h.clk.Now().Add(-800*time.Millisecond)), agyTool(1, "CALLING", cmd), agyTool(1, "DONE", cmd), agyResult("conv-e", "Generated the docs.")))
	sum := h.delegate(api.DelegateRequest{Title: "early write", Lane: []string{"src/"}})
	tk := h.waitStatus(sum.ID, StatusDone)
	if st := h.laneOf(tk, 1); st.State != api.StepRed || !slices.Equal(st.Files, []string{"docs/early.md"}) ||
		len(st.Unseen) != 0 {
		t.Fatalf("lane = %+v", st)
	}
}

// The lead edits a file outside the lane while no worker command runs: the worker's command ran (and is timed)
// before, so the change stays a warning.
func TestLaneEditOutsideRunsWarns(t *testing.T) {
	h := newHarness(t)
	laneRepo(h)
	h.agy("w", 1)
	cmd := `python fix.py`
	in := filepath.Join(h.cwd, "src", "x", "a.vue")
	lead := filepath.Join(h.cwd, "docs", "y.md")
	h.queue.Push(t, script(agyInit("conv-q"), agyTool(1, "CALLING", cmd), fakeWrite(in, "b\n"),
		fakeMtime(in, h.clk.Now()), agyTool(1, "DONE", cmd),
		fakeWrite(lead, "the lead's notes\n"), fakeMtime(lead, h.clk.Now().Add(time.Minute)),
		agyResult("conv-q", "Fixed a.vue.")))
	sum := h.delegate(api.DelegateRequest{Title: "lead edit", Lane: []string{"src/"}})
	tk := h.waitStatus(sum.ID, StatusDone)
	st := h.laneOf(tk, 1)
	if st.State != api.StepWarn || len(st.Files) != 0 || !slices.Equal(st.Unseen, []string{"docs/y.md"}) {
		t.Fatalf("lane = %+v", st)
	}
	if g := tk.LastTurn().Gate; g.State != api.GateGreen {
		t.Fatalf("gate = %+v", g)
	}
}

// A code runner whose start is not known (a result event without a call or a duration) writes where Seelegent
// cannot tell: an unexplained file outside the lane counts, and the step names the command.
func TestLaneUntimedRunnerFailsClosed(t *testing.T) {
	h := newHarness(t)
	laneRepo(h)
	h.agy("w", 1)
	out := filepath.Join(h.cwd, "docs", "z.md")
	h.queue.Push(t, script(agyInit("conv-r"), agyTool(1, "DONE", "npm run format"), fakeWrite(out, "z\n"),
		agyResult("conv-r", "Formatted.")))
	sum := h.delegate(api.DelegateRequest{Title: "untimed", Lane: []string{"src/"}})
	tk := h.waitStatus(sum.ID, StatusDone)
	if st := h.laneOf(tk, 1); st.State != api.StepRed || !slices.Equal(st.Files, []string{"docs/z.md"}) ||
		!strings.Contains(st.Tail, gate.UnknownNote+": npm run format") {
		t.Fatalf("lane = %+v", st)
	}
}
