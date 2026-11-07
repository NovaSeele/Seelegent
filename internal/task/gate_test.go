package task

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/proof"
)

// gateCheck is the fake gate command (the test binary run as "gate-check"): "<file> <want>" passes when the file
// (relative to the working folder) contains want; "--sleep <seconds>" waits.
func gateCheck(args []string) int {
	if len(args) == 2 && args[0] == "--sleep" {
		n, _ := strconv.Atoi(args[1])
		time.Sleep(time.Duration(n) * time.Second)
		return 0
	}
	if len(args) != 2 {
		fmt.Println("usage: gate-check <file> <want>")
		return 2
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Println("FAIL:", err)
		return 1
	}
	if !strings.Contains(string(data), args[1]) {
		fmt.Printf("FAIL: %s does not contain %q\n", args[0], args[1])
		return 1
	}
	fmt.Println("ok")
	return 0
}

// gateCmd is a gate command line that runs gateCheck with args.
func gateCmd(t *testing.T, args ...string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return `"` + exe + `" gate-check ` + strings.Join(args, " ")
}

func (h *harness) set(change func(*config.Settings)) {
	s := h.config()
	change(&s)
	h.settings.Store(&s)
}

// editScript is a turn that edits file (absolute) to text.
func editScript(conv, file, text, response string) string {
	return script(agyInit(conv), agyEdit(1, file), fakeWrite(file, text), agyResult(conv, response))
}

func TestGateRedFixTurnGreen(t *testing.T) {
	h := newHarness(t)
	h.set(func(s *config.Settings) { s.GateMaxAutoFixes = 1 })
	h.initRepo(map[string]string{"a.txt": "broken\n"})
	h.agy("w", 1)
	a := filepath.Join(h.cwd, "a.txt")
	h.queue.Push(t, editScript("conv-g", a, "still broken\n", "Changed a.txt."))
	h.queue.Push(t, editScript("conv-g", a, "fixed\n", "Fixed a.txt."))
	cmd := gateCmd(t, "a.txt", "fixed")
	sum := h.delegate(api.DelegateRequest{Title: "gated", GateCommands: []string{cmd}})
	tk := h.waitStatus(sum.ID, StatusDone)
	if len(tk.Turns) != 2 {
		t.Fatalf("turns = %d", len(tk.Turns))
	}
	first, fix := tk.Turns[0], tk.Turns[1]
	if first.Gate == nil || first.Gate.State != api.GateRed || first.Gate.AutoFix != 2 {
		t.Fatalf("first gate = %+v", first.Gate)
	}
	step := first.Gate.Steps[len(first.Gate.Steps)-1]
	if step.Cmd != cmd || step.State != api.StepRed || !strings.Contains(step.Tail, "does not contain") {
		t.Errorf("failing step = %+v", step)
	}
	if fix.By != ByGate || !strings.Contains(fix.Message, "does not contain") {
		t.Errorf("fix turn = %s %q", fix.By, fix.Message)
	}
	if fix.Gate == nil || fix.Gate.State != api.GateGreen {
		t.Fatalf("fix gate = %+v", fix.Gate)
	}
	if tk.Proof.AutoFixes != 1 || tk.GatePhase != nil {
		t.Errorf("proof = %+v phase = %+v", tk.Proof, tk.GatePhase)
	}
	if a := tk.Attention; a == nil || a.Reason != ReasonReview {
		t.Errorf("attention = %+v", tk.Attention)
	}
	sumNow, err := h.s.Summary(t.Context(), sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	if g := sumNow.Gate; g == nil || g.State != api.GateGreen || g.AutoFixes != 1 || g.Turn != 2 {
		t.Errorf("summary gate = %+v", sumNow.Gate)
	}
	rep, err := h.s.Report(t.Context(), sum.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Gate == nil || rep.Gate.State != api.GateGreen || rep.Gate.AutoFixTurns != 1 || rep.LastGate != nil {
		t.Errorf("report gate = %+v last = %+v", rep.Gate, rep.LastGate)
	}
	brief, err := h.s.Prompt(t.Context(), sum.ID, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(brief, "## Gate") || !strings.Contains(brief, "gate-check a.txt fixed") {
		t.Errorf("brief has no gate section:\n%s", brief)
	}
	evs := h.events(sum.ID)
	var phases []string
	for _, e := range eventsOf(evs, api.EventGate) {
		var d api.GateEventData
		decode(t, e, &d)
		phases = append(phases, d.Phase)
		if d.Phase == api.GateEventStart && (len(d.Plan) != len(d.Steps) || len(d.Plan) == 0 || d.Plan[len(d.Plan)-1].Cmd == "") {
			t.Errorf("start plan = %+v, steps = %v", d.Plan, d.Steps)
		}
	}
	if !slices.Contains(phases, api.GateEventStart) || !slices.Contains(phases, api.GateEventResult) {
		t.Errorf("gate events = %v", phases)
	}

	// A lead followup starts the fix budget again.
	h.queue.Push(t, editScript("conv-g", a, "fixed again\n", "Done."))
	if _, err := h.s.Followup(t.Context(), sum.ID, api.FollowupRequest{Message: "One more change."}); err != nil {
		t.Fatal(err)
	}
	tk = h.waitStatus(sum.ID, StatusDone)
	if tk.Proof.AutoFixes != 0 || tk.LastTurn().Gate == nil || tk.LastTurn().Gate.State != api.GateGreen {
		t.Errorf("after followup: proof = %+v gate = %+v", tk.Proof, tk.LastTurn().Gate)
	}
}

func TestGateRedWithoutBudgetAndLaneViolation(t *testing.T) {
	h := newHarness(t)
	h.initRepo(map[string]string{"a.txt": "a\n", "b.txt": "b\n"})
	h.agy("w", 1)
	a := filepath.Join(h.cwd, "a.txt")
	h.queue.Push(t, editScript("conv-l", a, "changed\n", "Changed a.txt."))
	sum := h.delegate(api.DelegateRequest{Title: "lane", Lane: []string{"b.txt"}})
	tk := h.waitStatus(sum.ID, StatusDone)
	g := tk.LastTurn().Gate
	if g == nil || g.State != api.GateRed || g.Steps[0].Kind != api.StepLane || !slices.Equal(g.Steps[0].Files, []string{"a.txt"}) {
		t.Fatalf("gate = %+v", g)
	}
	if a := tk.Attention; a == nil || a.Reason != ReasonGateRed || a.Detail != "files outside the lane: a.txt" {
		t.Errorf("attention = %+v", tk.Attention)
	}
	rep, err := h.s.Report(t.Context(), sum.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Lane == nil || !slices.Equal(rep.Lane.OutOfLane, []string{"a.txt"}) || rep.Gate == nil ||
		rep.Gate.State != api.GateRed || len(rep.Gate.Steps) != 1 {
		t.Errorf("report lane = %+v gate = %+v", rep.Lane, rep.Gate)
	}
	if len(eventsOf(h.events(sum.ID), api.EventLane)) != 1 {
		t.Error("no lane violation event")
	}
}

func TestLaneClaims(t *testing.T) {
	h := newHarness(t)
	if err := mkdir(filepath.Join(h.cwd, "src")); err != nil {
		t.Fatal(err)
	}
	h.initRepo(map[string]string{"src/x.go": "package x\n"})
	// No account: the tasks stay pending and hold their claims.
	a := h.delegate(api.DelegateRequest{Title: "A", Lane: []string{"src"}})
	_, err := h.s.Delegate(t.Context(), api.DelegateRequest{Prompt: "x", Cwd: h.cwd, Lane: []string{"src/x.go"}})
	var lc *LaneConflictError
	if !errors.Is(err, ErrConflict) || !errors.As(err, &lc) || lc.Conflicts[0].TaskID != a.ID {
		t.Fatalf("overlapping lane = %v", err)
	}
	if d := lc.APIDetails(); d["reason"] != "lane_conflict" {
		t.Errorf("details = %v", d)
	}
	b := h.delegate(api.DelegateRequest{Title: "B", Lane: []string{"src/x.go"}, ForceLane: true})
	if tk := h.task(b.ID); !slices.Equal(tk.Proof.Lane.Forced, []string{a.ID}) || tk.Proof.Lane.Paths[0] != "src/x.go" {
		t.Errorf("forced lane = %+v", tk.Proof.Lane)
	}
	if lane := h.task(a.ID).Proof.Lane; !slices.Equal(lane.Paths, []string{"src/"}) {
		t.Errorf("folder lane = %v", lane.Paths)
	}
	h.set(func(s *config.Settings) { s.Lanes = config.LanesWarn })
	c := h.delegate(api.DelegateRequest{Title: "C", Lane: []string{"src/*.go"}})
	var d api.LaneEventData
	evs := eventsOf(h.events(c.ID), api.EventLane)
	if len(evs) != 1 {
		t.Fatalf("lane events = %d", len(evs))
	}
	decode(t, evs[0], &d)
	if d.Kind != api.LaneWarning || len(d.Conflicts) != 2 {
		t.Errorf("warning = %+v", d)
	}
	view, err := h.s.Lanes(t.Context(), h.cwd)
	if err != nil {
		t.Fatal(err)
	}
	if view.Mode != config.LanesWarn || len(view.Claims) != 3 {
		t.Errorf("lanes = %+v", view)
	}
	counters, err := proof.NewLedger(h.db, nil).Counters(t.Context(), h.task(a.ID).Proof.Project)
	if err != nil {
		t.Fatal(err)
	}
	if counters[proof.CounterLaneRefusals] != 1 || counters[proof.CounterLaneForced] != 1 {
		t.Errorf("counters = %v", counters)
	}
	// A lane that leaves the repository is refused.
	if _, err := h.s.Delegate(t.Context(), api.DelegateRequest{Prompt: "x", Cwd: h.cwd, Lane: []string{"../y"}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("escaping lane = %v", err)
	}
}

func TestVerifierRejectThenAccept(t *testing.T) {
	h := newHarness(t)
	h.set(func(s *config.Settings) { s.GateMaxAutoFixes = 1; s.Verifier = true })
	h.initRepo(map[string]string{"a.txt": "one\n"})
	h.agy("w", 1)
	a := filepath.Join(h.cwd, "a.txt")
	h.queue.Push(t, editScript("conv-p", a, "two\n", "Changed a.txt."))
	h.queue.Push(t, okScript("conv-v1", "## Verdict\nreject\n\n## Reasons\n- a.txt:1 - the value must be three\n"))
	h.queue.Push(t, editScript("conv-p", a, "three\n", "Fixed a.txt."))
	h.queue.Push(t, okScript("conv-v2", "## Verdict\naccept\n\n## Reasons\n"))
	sum := h.delegate(api.DelegateRequest{Title: "verified"})
	tk := h.waitStatus(sum.ID, StatusDone)
	if len(tk.Turns) != 2 {
		t.Fatalf("turns = %d", len(tk.Turns))
	}
	v1, v2 := tk.Turns[0].Verifier, tk.Turns[1].Verifier
	if v1 == nil || v1.Verdict != api.VerdictReject || len(v1.Reasons) != 1 || len(v1.Cites) != 1 ||
		v1.Cites[0].State != proof.CiteLocated {
		t.Fatalf("first verdict = %+v", v1)
	}
	if tk.Turns[1].By != ByVerifier || !strings.Contains(tk.Turns[1].Message, "must be three") {
		t.Errorf("fix turn = %s %q", tk.Turns[1].By, tk.Turns[1].Message)
	}
	if v2 == nil || v2.Verdict != api.VerdictAccept {
		t.Fatalf("second verdict = %+v", v2)
	}
	child := h.task(v1.ChildID)
	if child.Kind != KindVerify || child.ParentID != sum.ID || child.Verdict == nil || child.Review == nil ||
		child.Mode != ModePlan {
		t.Errorf("verify task = %+v", child)
	}
	prompt, err := h.s.Prompt(t.Context(), child.ID, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "## Verdict") || !strings.Contains(prompt, "a.txt") ||
		strings.Contains(prompt, "## Your role") {
		t.Errorf("verifier prompt:\n%s", prompt)
	}
	page, err := h.s.List(t.Context(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != sum.ID {
		t.Errorf("listed %d tasks", len(page.Items))
	}
	rep, err := h.s.Report(t.Context(), sum.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verifier == nil || rep.Verifier.Verdict != api.VerdictAccept {
		t.Errorf("report verifier = %+v", rep.Verifier)
	}
	stats, err := h.s.ProofStats(t.Context(), h.cwd, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Verifier.Runs != 2 || stats.Verifier.Rejects != 1 || stats.Verifier.Accepts != 1 ||
		stats.AutoFixTurns != 1 || stats.GatedTasks != 1 {
		t.Errorf("stats = %+v", stats)
	}
}

// A worker that rewrites the Gate: line of its spec task changes only the reported bar: the gate runs the command
// frozen when the task started (old bug D10).
func TestFrozenGateIgnoresWorkerEdits(t *testing.T) {
	h := newHarness(t)
	h.set(func(s *config.Settings) { s.Lanes = config.LanesWarn })
	h.agy("w", 1)
	frozen := gateCmd(t, "b.txt", "ok")
	planted := gateCmd(t, "b.txt", "never")
	if err := mkdir(filepath.Join(h.cwd, "specs", "001-x")); err != nil {
		t.Fatal(err)
	}
	tasksMD := "# Tasks\n\n- [ ] T001 Check `b.txt` Gate: `%s`\n"
	h.initRepo(map[string]string{"b.txt": "ok\n", "specs/001-x/tasks.md": fmt.Sprintf(tasksMD, frozen)})
	h.queue.Push(t, editScript("conv-f", filepath.Join(h.cwd, "specs", "001-x", "tasks.md"),
		fmt.Sprintf(tasksMD, planted), "Updated the gate."))
	sum := h.delegate(api.DelegateRequest{Title: "frozen", Spec: &api.SpecRequest{Dir: "specs/001-x", TaskIDs: []string{"T001"}}})
	tk := h.waitStatus(sum.ID, StatusDone)
	g := tk.LastTurn().Gate
	if g == nil || g.State != api.GateGreen || len(tk.Turns) != 1 {
		t.Fatalf("gate = %+v (turns %d)", g, len(tk.Turns))
	}
	for _, s := range g.Steps {
		if strings.Contains(s.Cmd, "never") {
			t.Errorf("the gate ran the worker's command: %+v", s)
		}
	}
	if len(tk.Proof.BarChanged) == 0 {
		t.Errorf("bar changed = %v", tk.Proof.BarChanged)
	}
}

func TestSpecGateAndLedger(t *testing.T) {
	h := newHarness(t)
	h.agy("w", 1)
	cmd := gateCmd(t, "a.txt", "done")
	feature := gateCmd(t, "b.txt", "ok")
	if err := mkdir(filepath.Join(h.cwd, "specs", "001-x")); err != nil {
		t.Fatal(err)
	}
	h.initRepo(map[string]string{"a.txt": "todo\n", "b.txt": "ok\n",
		"specs/001-x/tasks.md": "# Tasks\n\nFeature gate: `" + feature + "`\n\n" +
			"- [ ] T001 Write `a.txt` Gate: `" + cmd + "`\n- [ ] T002 Other work in `c.txt`\n"})
	if _, err := h.s.Delegate(t.Context(), api.DelegateRequest{Prompt: "x", Cwd: h.cwd,
		Spec: &api.SpecRequest{Dir: "specs/001-x", TaskIDs: []string{"T999"}}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown spec task = %v", err)
	}
	a := filepath.Join(h.cwd, "a.txt")
	h.queue.Push(t, editScript("conv-s", a, "done\n", "Did T001."))
	sum := h.delegate(api.DelegateRequest{Title: "spec", Spec: &api.SpecRequest{Dir: "specs/001-x", TaskIDs: []string{"T001"}}})
	tk := h.waitStatus(sum.ID, StatusDone)
	p := tk.Proof
	if p.Lane == nil || p.Lane.Source != "spec" || !slices.Equal(p.Lane.Paths, []string{"a.txt"}) {
		t.Errorf("lane = %+v", p.Lane)
	}
	if len(p.Commands) != 2 || p.Commands[0].Cmd != cmd || p.Commands[1].Cmd != feature {
		t.Errorf("commands = %+v", p.Commands)
	}
	if g := tk.LastTurn().Gate; g == nil || g.State != api.GateGreen {
		t.Fatalf("gate = %+v", g)
	}
	entries, err := proof.NewLedger(h.db, nil).Entries(t.Context(), p.Project, p.FeatureKey)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := entries["T001"]; !ok || e.State != api.GateGreen || e.Ref != sum.ID || len(e.Cmds) != 2 {
		t.Errorf("ledger = %+v", entries)
	}
	brief, err := h.s.Prompt(t.Context(), sum.ID, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(brief, "## Your lane") || !strings.Contains(brief, "a.txt") {
		t.Errorf("brief has no lane:\n%s", brief)
	}
}

func TestCancelDuringGate(t *testing.T) {
	h := newHarness(t)
	h.initRepo(map[string]string{"a.txt": "a\n"})
	h.agy("w", 1)
	a := filepath.Join(h.cwd, "a.txt")
	h.queue.Push(t, editScript("conv-c", a, "b\n", "Changed."))
	sum := h.delegate(api.DelegateRequest{Title: "slow gate", GateCommands: []string{gateCmd(t, "--sleep", "30")}})
	h.waitFor("the gate command to run", func() bool {
		tk := h.task(sum.ID)
		if tk.GatePhase == nil || tk.LastTurn().Gate == nil {
			return false
		}
		for _, st := range tk.LastTurn().Gate.Steps {
			if st.Kind == api.StepTask && st.State == api.StepRunning {
				return true
			}
		}
		return false
	})
	if _, err := h.s.Takeover(t.Context(), sum.ID); !errors.Is(err, ErrBadState) {
		t.Errorf("takeover during the gate = %v", err)
	}
	if _, _, err := h.s.Cancel(t.Context(), sum.ID, api.CancelRequest{WaitSeconds: 20}); err != nil {
		t.Fatal(err)
	}
	tk := h.waitStatus(sum.ID, StatusCancelled)
	if g := tk.LastTurn().Gate; g == nil || g.State != api.GateSkipped || tk.GatePhase != nil {
		t.Errorf("gate after cancel = %+v phase = %+v", g, tk.GatePhase)
	}
}

func TestGateRunsAgainAfterRestart(t *testing.T) {
	h := newHarness(t)
	h.initRepo(map[string]string{"a.txt": "a\n"})
	h.agy("w", 1)
	a := filepath.Join(h.cwd, "a.txt")
	h.queue.Push(t, editScript("conv-r", a, "b\n", "Changed."))
	sum := h.delegate(api.DelegateRequest{Title: "restart", GateCommands: []string{gateCmd(t, "--sleep", "2")}})
	h.waitFor("the gate phase", func() bool { return h.task(sum.ID).GatePhase != nil })
	h.stop()
	if tk := h.task(sum.ID); tk.GatePhase == nil || tk.Status != StatusRunning {
		t.Fatalf("after stop: status %s phase %+v", tk.Status, tk.GatePhase)
	}
	h.start()
	tk := h.waitStatus(sum.ID, StatusDone)
	if g := tk.LastTurn().Gate; g == nil || g.State != api.GateGreen {
		t.Errorf("gate after restart = %+v", g)
	}
}

func TestGateDetail(t *testing.T) {
	exit := 1
	g := &api.GateResult{State: api.GateRed, Steps: []api.GateStep{
		{Kind: api.StepLane, Label: "lane", State: api.StepGreen},
		{Kind: api.StepAuto, Label: "syntax", State: api.StepRed,
			Failures: []api.GateFailure{{File: "a.json", Message: "bad"}, {File: "b.go", Message: "bad"}}},
		{Kind: api.StepTask, Label: "T001", Cmd: "npm test", Dir: "web", State: api.StepRed, Exit: &exit},
		{Kind: api.StepTask, Label: "go vet", Cmd: "go vet", State: api.StepError},
		{Kind: api.StepDoctrine, Label: "api", Cmd: "make api", State: api.StepRed, Foreign: true, CausedBy: []string{"t9"}},
		{Kind: api.StepDoctrine, Label: "no-todo", Cmd: "check", State: api.StepRed},
	}}
	want := "syntax check failed: a.json, b.go; command failed: cd web && npm test; command could not run: go vet; " +
		"doctrine check api failed: make api (may be caused by t9); doctrine check no-todo failed: check"
	if got := gateDetail(g); got != want {
		t.Errorf("gateDetail = %q, want %q", got, want)
	}
	if got := withList("files outside the lane", []string{"a", "b", "c", "d", "e"}); got != "files outside the lane: a, b, c and 2 more" {
		t.Errorf("withList = %q", got)
	}
}
