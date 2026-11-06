package task

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/launch"
)

func TestComputeAttention(t *testing.T) {
	now := harnessEpoch.Add(time.Hour)
	set := config.Defaults()
	set.StallMinutes, set.StallToolMinutes, set.AttentionDecayHours = 10, 20, 24
	done := func(resp string) *Task {
		return &Task{Status: StatusDone, Kind: KindNormal, UpdatedAt: now,
			Turns: []*Turn{{N: 1, Response: resp, EndedAt: now.Add(-time.Minute), DeniedActions: []string{"git push"}}}}
	}
	running := &Task{Status: StatusRunning, Kind: KindNormal, ConversationID: "c"}
	cases := []struct {
		name          string
		t             *Task
		live          liveState
		state, reason string
	}{
		{"review", done("All good."), liveState{}, api.AttentionInfo, ReasonReview},
		{"no response", done(" "), liveState{}, api.AttentionBlocked, ReasonNoResponse},
		{"question", done("## Blocked\nWhich one?\n- a\n- b"), liveState{}, api.AttentionQuestion, ReasonQuestion},
		{"reviewed", func() *Task { x := done("ok"); x.Review = &api.Review{Verdict: "accepted"}; return x }(),
			liveState{}, "", ""},
		{"decayed", func() *Task { x := done("ok"); x.Turns[0].EndedAt = now.Add(-25 * time.Hour); return x }(),
			liveState{}, "", ""},
		{"dismissed", func() *Task { x := done("ok"); x.AttentionDismissedAt = now; return x }(), liveState{}, "", ""},
		{"failed", &Task{Status: StatusFailed, Kind: KindNormal, Error: "boom", UpdatedAt: now}, liveState{},
			api.AttentionBlocked, ReasonFailed},
		{"cancelled", &Task{Status: StatusCancelled, Kind: KindNormal}, liveState{}, "", ""},
		{"deps blocked", &Task{Status: StatusBlockedDeps, Kind: KindNormal, DepsReason: "x"}, liveState{},
			api.AttentionBlocked, ReasonDepsBlocked},
		{"no accounts", &Task{Status: StatusWaitingQuota, Kind: KindNormal, Waiting: "no_account"}, liveState{},
			api.AttentionBlocked, ReasonNoAccounts},
		{"resting", &Task{Status: StatusWaitingQuota, Kind: KindNormal, Waiting: "resting"}, liveState{}, "", ""},
		{"running fresh", running, liveState{running: true, sawInit: true, lastEvent: now.Add(-time.Minute)}, "", ""},
		{"not followed", running, liveState{}, "", ""},
		{"quiet", running, liveState{running: true, sawInit: true, lastEvent: now.Add(-11 * time.Minute)},
			api.AttentionStalled, ReasonStalled},
		{"long tool ok", running, liveState{running: true, sawInit: true, tool: "run_command",
			lastEvent: now.Add(-15 * time.Minute)}, "", ""},
		{"long tool", running, liveState{running: true, sawInit: true, tool: "run_command", command: "npm test",
			lastEvent: now.Add(-21 * time.Minute)}, api.AttentionStalled, ReasonStalled},
		{"no init", &Task{Status: StatusRunning, Kind: KindNormal}, liveState{running: true,
			started: now.Add(-4 * time.Minute), lastEvent: now}, api.AttentionStalled, ReasonStalled},
		{"background", running, liveState{running: true, sawInit: true, waiting: now.Add(-time.Hour)},
			api.AttentionBackgroundWait, ReasonBackgroundWait},
		{"manual", &Task{Status: StatusRunning, Kind: KindNormal, Manual: true}, liveState{}, "", ""},
		{"other kind", &Task{Status: StatusFailed, Kind: "contest"}, liveState{}, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := computeAttention(c.t, c.live, now, set)
			switch {
			case c.state == "" && got != nil:
				t.Fatalf("got %+v, want none", got)
			case c.state != "" && (got == nil || got.State != c.state || got.Reason != c.reason):
				t.Fatalf("got %+v, want %s/%s", got, c.state, c.reason)
			}
		})
	}
	// An unchanged flag keeps its Since.
	x := done("ok")
	x.Attention = &api.Attention{State: api.AttentionInfo, Reason: ReasonReview, Since: "2026-11-07T08:00:00.000Z"}
	if got := computeAttention(x, liveState{}, now, set); got.Since != "2026-11-07T08:00:00.000Z" {
		t.Fatalf("since = %s", got.Since)
	}
	if !strings.Contains(computeAttention(running, liveState{running: true, sawInit: true, tool: "run_command",
		command: "npm test", lastEvent: now.Add(-21 * time.Minute)}, now, set).Detail, `"npm test"`) {
		t.Fatal("the stall detail does not name the command")
	}
}

func TestParseBlocked(t *testing.T) {
	cases := map[string]*api.ReportQuestion{
		"no heading":                                 nil,
		"## Blocked\n\n## Next\nx":                   nil,
		"Intro\n## Blocked\nWhich DB?\n- A\n* B\n":   {Text: "Which DB?", Options: []string{"A", "B"}},
		"## Blocked\r\nNeed a key.\r\n## Notes\r\n-": {Text: "Need a key.", Options: []string{}},
		"## Blocked\n- only options":                 {Text: "", Options: []string{"only options"}},
	}
	for in, want := range cases {
		got := parseBlocked(in)
		if (got == nil) != (want == nil) || got != nil && (got.Text != want.Text || !slices.Equal(got.Options, want.Options)) {
			t.Errorf("parseBlocked(%q) = %+v, want %+v", in, got, want)
		}
	}
}

func TestBus(t *testing.T) {
	b := NewBus()
	b.Publish() // nothing to publish: no wake-up
	ch := b.Changed()
	sub := b.Subscribe(2)
	small := b.Subscribe(1)
	b.Publish(Notice{Kind: NoticeTask, TaskID: "a"}, Notice{Kind: NoticeTask, TaskID: "b"})
	select {
	case <-ch:
	default:
		t.Fatal("Changed not closed")
	}
	if n := <-sub.C(); n.TaskID != "a" || sub.Overflowed() {
		t.Fatalf("notice = %+v", n)
	}
	if !small.Overflowed() {
		t.Fatal("a full subscriber is not marked")
	}
	<-small.C()
	if _, ok := <-small.C(); ok {
		t.Fatal("an overflowed subscription stays open")
	}
	small.Close() // already closed by the bus: no panic
	sub.Close()
	sub.Close()
	b.Publish(Notice{Kind: NoticeTask, TaskID: "c"})
}

func TestClassify(t *testing.T) {
	cases := []struct {
		o      backend.Outcome
		exit   launch.Exit
		status string
	}{
		{backend.Outcome{Status: backend.OutcomeOK}, launch.Exit{}, AttemptOK},
		{backend.Outcome{Status: backend.OutcomeCanceled}, launch.Exit{}, AttemptCancelled},
		{backend.Outcome{Status: backend.OutcomeBackgroundWait}, launch.Exit{}, AttemptBackgroundWait},
		{backend.Outcome{Status: backend.OutcomeFailed}, launch.Exit{Reason: launch.ReasonTimeout}, AttemptError},
		{backend.Outcome{Status: backend.OutcomeFailed}, launch.Exit{Reason: launch.ReasonLost}, AttemptTransient},
		{backend.Outcome{Status: backend.OutcomeFailed, Class: event.ClassQuotaExhausted}, launch.Exit{}, AttemptQuota},
		{backend.Outcome{Status: backend.OutcomeFailed, Class: event.ClassAuthRequired}, launch.Exit{}, AttemptAuth},
		{backend.Outcome{Status: backend.OutcomeFailed, Class: event.ClassTransient}, launch.Exit{}, AttemptTransient},
		{backend.Outcome{Status: backend.OutcomeFailed, Class: event.ClassFatal}, launch.Exit{}, AttemptError},
	}
	for _, c := range cases {
		status, text := classify(c.o, c.exit, 30)
		if status != c.status {
			t.Errorf("classify(%+v, %+v) = %s", c.o, c.exit, status)
		}
		if c.exit.Reason == launch.ReasonTimeout && !strings.Contains(text, "30 min") {
			t.Errorf("timeout text = %q", text)
		}
	}
}

func TestExcludedAndClassOf(t *testing.T) {
	tu := &Turn{Attempts: []*Attempt{
		{AccountID: "a", Status: AttemptAuth}, {AccountID: "b", Status: AttemptTransient},
		{AccountID: "b", Status: AttemptTransient}, {AccountID: "b", Status: AttemptTransient},
		{AccountID: "c", Status: AttemptTransient}, {AccountID: "a", Status: AttemptAuth},
	}}
	if got := excluded(tu); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("excluded = %v", got)
	}
	if classOf(nil) != "" || classOf(&Attempt{Status: AttemptQuota}) != event.ClassQuotaExhausted ||
		classOf(&Attempt{Status: AttemptOK}) != "" {
		t.Fatal("classOf")
	}
	if lastError(&Turn{}) == "" || lastError(&Turn{Attempts: []*Attempt{{Error: "x"}, {}}}) != "x" {
		t.Fatal("lastError")
	}
}

// TestPanicFailsTask turns a panic in engine work into a failed task instead of a crashed daemon (old B2).
func TestPanicFailsTask(t *testing.T) {
	h := newHarness(t)
	sum := h.delegate(api.DelegateRequest{After: nil})
	if _, _, err := h.s.Cancel(t.Context(), sum.ID, api.CancelRequest{WaitSeconds: 10}); err != nil {
		t.Fatal(err)
	}
	h.waitStatus(sum.ID, StatusCancelled)
	// A followup reopens it; a panicking goroutine then fails it.
	if _, err := h.s.Followup(t.Context(), sum.ID, api.FollowupRequest{Message: "again"}); err != nil {
		t.Fatal(err)
	}
	h.s.spawn("test", sum.ID, func() { panic("parser exploded") })
	h.waitFor("the failure", func() bool {
		tk := h.task(sum.ID)
		return tk.Status == StatusFailed && strings.Contains(tk.Error, "parser exploded")
	})
	if _, _, err := h.s.Cancel(t.Context(), sum.ID, api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
}

func TestReportShapes(t *testing.T) {
	s := &Service{o: Options{}}
	cwd := t.TempDir()
	var cmds []string
	for i := range 8 {
		cmds = append(cmds, fmt.Sprintf("cmd %d %s", i, strings.Repeat("x", 200)))
	}
	tk := &Task{ID: "t1", Status: StatusDone, Cwd: cwd, Effort: "high", Model: "m",
		Spec: &api.SpecRef{Dir: "specs/001", TaskIDs: []string{"T1"}}, Profile: &api.ProfileRef{ID: "r"},
		Turns: []*Turn{
			{N: 1, BlockedCommands: []string{"git push"}},
			{N: 2, Response: "See [handler](file:///" + filepath.ToSlash(cwd) + "/a.go) now.", Files: []string{"a.go",
				filepath.Join(filepath.Dir(cwd), "outside.go")}, Commands: cmds, BlockedCommands: []string{"git push", "rm -rf"},
				Attempts: []*Attempt{{AccountID: "acc1", Status: AttemptQuota, Error: strings.Repeat("e", 400)},
					{AccountID: "acc2", AccountLabel: "second", Status: AttemptOK}}},
		},
		Attention: &api.Attention{State: api.AttentionInfo, Reason: ReasonReview},
	}
	rep := s.report(tk, "")
	if rep.Response != "See handler now." {
		t.Errorf("response = %q", rep.Response)
	}
	if len(rep.CommandsRun) != reportCommands || rep.CommandsTotal != 8 || len([]rune(rep.CommandsRun[0])) != reportCommandChars {
		t.Errorf("commands = %d/%d", len(rep.CommandsRun), rep.CommandsTotal)
	}
	if rep.FilesChanged[0] != "a.go" || !filepath.IsAbs(filepath.FromSlash(rep.FilesChanged[1])) {
		t.Errorf("files = %v", rep.FilesChanged)
	}
	if !slices.Equal(rep.BlockedCommands, []string{"git push", "rm -rf"}) || rep.Attention != nil {
		t.Errorf("blocked = %v attention = %+v", rep.BlockedCommands, rep.Attention)
	}
	if len(rep.Attempts) != 2 || len(rep.Attempts[0].Error) != reportErrorChars || rep.Effort != "high" ||
		rep.Spec == nil || rep.Profile != "r" {
		t.Errorf("report = %+v", rep)
	}
}

func TestDelegateValidation(t *testing.T) {
	h := newHarness(t)
	rel := "relative/dir"
	cases := []api.DelegateRequest{
		{Prompt: " ", Cwd: h.cwd},
		{Prompt: "x", Cwd: rel},
		{Prompt: "x", Cwd: filepath.Join(h.cwd, "missing")},
		{Prompt: "x", Cwd: h.cwd, Mode: "wild"},
		{Prompt: "x", Cwd: h.cwd, Member: "bob"},
		{Prompt: "x", Cwd: h.cwd, Tier: "huge"},
		{Prompt: "x", Cwd: h.cwd, Provider: "nope"},
		{Prompt: "x", Cwd: h.cwd, Role: "nope"},
		{Prompt: "x", Cwd: h.cwd, Spec: &api.SpecRequest{Dir: "specs/none"}},
		{Prompt: "x", Cwd: h.cwd, Spec: &api.SpecRequest{}},
		{Prompt: "x", Cwd: h.cwd, After: []string{"a"}, AfterMode: "whenever"},
	}
	for _, c := range cases {
		if _, err := h.s.Delegate(t.Context(), c); !errors.Is(err, ErrInvalid) {
			t.Errorf("Delegate(%+v) = %v, want invalid", c, err)
		}
	}
	if _, err := h.s.Delegate(t.Context(), api.DelegateRequest{Prompt: "x", Cwd: h.cwd, Backend: "nope"}); err == nil {
		t.Error("unknown backend accepted")
	}
	if _, err := h.s.Summary(t.Context(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown task: %v", err)
	}
	if got := title("", "# "+strings.Repeat("long ", 30)+"\nsecond"); len([]rune(got)) != titleChars ||
		!strings.HasSuffix(got, "…") {
		t.Errorf("title = %q", got)
	}
	if title("Given", "prompt") != "Given" {
		t.Error("given title ignored")
	}
}

func TestDelegateSpecAndPlan(t *testing.T) {
	h := newHarness(t)
	inside := filepath.Join(h.cwd, "specs", "001-x")
	outside := filepath.Join(h.dir, "workspace", "specs", "002-y")
	skill := filepath.Join(h.dir, "workspace", ".agents", "skills", "speckit-implement")
	for _, d := range []string{inside, outside, skill} {
		if err := mkdir(d); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeText(filepath.Join(skill, "SKILL.md"), "skill"); err != nil {
		t.Fatal(err)
	}
	a := h.delegate(api.DelegateRequest{Mode: ModePlan, Spec: &api.SpecRequest{Dir: inside, TaskIDs: []string{"T1"}}})
	if a.Spec == nil || a.Spec.Dir != "specs/001-x" || a.Mode != ModePlan {
		t.Fatalf("inside spec = %+v", a.Spec)
	}
	b := h.delegate(api.DelegateRequest{Spec: &api.SpecRequest{Dir: outside}})
	tb := h.task(b.ID)
	if !tb.SpecOutside {
		t.Fatalf("outside spec = %+v", tb.Spec)
	}
	if sp := h.s.briefSpec(tb); sp == nil || !strings.HasSuffix(sp.ImplementSkill, "speckit-implement/SKILL.md") {
		t.Fatalf("brief spec = %+v", sp)
	}
	for _, id := range []string{a.ID, b.ID} {
		if _, _, err := h.s.Cancel(t.Context(), id, api.CancelRequest{WaitSeconds: 10}); err != nil {
			t.Fatal(err)
		}
	}
	// Follow-up task ids need a spec; other followups take them.
	h.agy("only", 0)
	h.queue.Push(t, okScript("conv-s", "ok"))
	if _, err := h.s.Followup(t.Context(), b.ID, api.FollowupRequest{Message: "next", TaskIDs: []string{"T2"}}); err != nil {
		t.Fatal(err)
	}
	tb = h.waitStatus(b.ID, StatusDone)
	if !slices.Equal(tb.Spec.TaskIDs, []string{"T2"}) || !tb.LastTurn().TaskIDsPassed {
		t.Fatalf("spec = %+v", tb.Spec)
	}
}
