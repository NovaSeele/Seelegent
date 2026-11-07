package hook

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/store"
	"github.com/NovaSeele/Seelegent/internal/task"
)

type fakeTasks struct {
	mu  sync.Mutex
	bus *task.Bus
	m   map[string]api.TaskDetail
}

func (f *fakeTasks) Bus() *task.Bus { return f.bus }

func (f *fakeTasks) Get(_ context.Context, id string) (api.TaskDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.m[id]
	if !ok {
		return api.TaskDetail{}, task.ErrNotFound
	}
	return d, nil
}

func (f *fakeTasks) set(d api.TaskDetail, created bool) {
	f.mu.Lock()
	f.m[d.ID] = d
	f.mu.Unlock()
	f.bus.Publish(task.Notice{Kind: task.NoticeTask, TaskID: d.ID, Created: created})
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func echoCmd() string {
	if runtime.GOOS == "windows" {
		return "echo event=%SEELEGENT_EVENT% task=%SEELEGENT_TASK_ID% port=%SEELEGENT_PORT%& more"
	}
	return `echo "event=$SEELEGENT_EVENT task=$SEELEGENT_TASK_ID port=$SEELEGENT_PORT"; cat`
}

func sleepCmd() string {
	if runtime.GOOS == "windows" {
		return "ping -n 30 127.0.0.1 >nul"
	}
	return "sleep 30"
}

func setup(t *testing.T) (*Service, *fakeTasks, chan api.HookRun, *syncBuf) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "seelegent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ft := &fakeTasks{bus: task.NewBus(), m: map[string]api.TaskDetail{}}
	ran := make(chan api.HookRun, 32)
	log := &syncBuf{}
	s := New(Options{DB: db, Tasks: ft, Port: 47858, Log: log, Ran: func(r api.HookRun) { ran <- r },
		Redact: func(s string) string { return strings.ReplaceAll(s, "sekrit", "[REDACTED]") }})
	t.Cleanup(s.Close)
	return s, ft, ran, log
}

func TestReplaceKeepsIDs(t *testing.T) {
	s, _, _, _ := setup(t)
	ctx := t.Context()
	got, err := s.Replace(ctx, []api.Hook{{Name: "one", On: []string{api.HookTaskDone}, Run: "echo 1", Enabled: true},
		{On: []string{api.HookTaskFailed, api.HookTaskFailed}, Run: "echo 2"}})
	if err != nil || len(got) != 2 || got[0].ID == "" || got[0].TimeoutSeconds != DefaultTimeout ||
		len(got[1].On) != 1 {
		t.Fatalf("replace = %+v %v", got, err)
	}
	// Reordered and edited: the ids stay with their hooks.
	got2, err := s.Replace(ctx, []api.Hook{{ID: got[1].ID, On: []string{api.HookTaskDone}, Run: "echo two"}, got[0]})
	if err != nil || got2[0].ID != got[1].ID || got2[1].ID != got[0].ID {
		t.Fatalf("reorder = %+v %v", got2, err)
	}
	for _, bad := range []api.Hook{
		{On: []string{"task.exploded"}, Run: "x"}, {On: []string{api.HookTaskDone}}, {Run: "x"},
		{On: []string{api.HookTaskDone}, Run: "a\nb"}, {On: []string{api.HookTaskDone}, Run: "x", TimeoutSeconds: 601},
		{On: []string{api.HookTaskDone}, Run: "x", Project: "rel"}, {ID: "hnope", On: []string{api.HookTaskDone}, Run: "x"},
	} {
		if _, err := s.Replace(ctx, []api.Hook{bad}); !errors.Is(err, ErrInvalid) {
			t.Errorf("Replace(%+v) = %v", bad, err)
		}
	}
	if list, _ := s.List(ctx); len(list) != 2 {
		t.Errorf("a refused replace changed the list: %+v", list)
	}
}

// TestImportOnce: an imported hook that is already there (same command, events, project) is not added again.
func TestImportOnce(t *testing.T) {
	s, _, _, _ := setup(t)
	ctx := t.Context()
	if _, err := s.Replace(ctx, []api.Hook{{On: []string{api.HookTaskDone}, Run: "echo 1", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	old := []api.Hook{
		{On: []string{api.HookTaskDone, api.HookTaskDone}, Run: " echo 1 "},
		{On: []string{api.HookTaskFailed, api.HookTaskDone}, Run: "echo 2"},
		{On: []string{"nope"}, Run: "echo 3"},
	}
	added, unchanged, skipped, err := s.Import(ctx, old)
	if err != nil || added != 1 || unchanged != 1 || len(skipped) != 1 {
		t.Fatalf("import = %d %d %v %v", added, unchanged, skipped, err)
	}
	added, unchanged, _, err = s.Import(ctx, []api.Hook{{On: []string{api.HookTaskDone, api.HookTaskFailed},
		Run: "echo 2"}})
	if err != nil || added != 0 || unchanged != 1 {
		t.Fatalf("second import = %d %d %v", added, unchanged, err)
	}
	if list, _ := s.List(ctx); len(list) != 2 {
		t.Errorf("list = %+v", list)
	}
}

func TestTestRunsByIDWithStdinAndEnv(t *testing.T) {
	s, _, ran, log := setup(t)
	ctx := t.Context()
	hooks, err := s.Replace(ctx, []api.Hook{{On: []string{api.HookTaskDone}, Run: "echo first"},
		{On: []string{api.HookTaskDone}, Run: echoCmd() + " && echo sekrit"}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Test(ctx, hooks[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode == nil || *r.ExitCode != 0 || !r.Test || r.HookID != hooks[1].ID {
		t.Fatalf("run = %+v", r)
	}
	if !strings.Contains(r.Output, "event=hook.test") || !strings.Contains(r.Output, "port=47858") ||
		!strings.Contains(r.Output, `"test":true`) || strings.Contains(r.Output, "sekrit") {
		t.Errorf("output = %q", r.Output)
	}
	<-ran
	if !strings.Contains(log.String(), "hook="+hooks[1].ID) {
		t.Errorf("hooks.log = %q", log.String())
	}
	if _, err := s.Test(ctx, "hmissing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id = %v", err)
	}
}

func TestTimeoutKills(t *testing.T) {
	s, _, _, _ := setup(t)
	hooks, err := s.Replace(t.Context(), []api.Hook{{On: []string{api.HookTaskDone}, Run: sleepCmd(), TimeoutSeconds: 1}})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	r, err := s.Test(t.Context(), hooks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !r.TimedOut || time.Since(start) > 15*time.Second {
		t.Errorf("run = %+v after %v", r, time.Since(start))
	}
}

func TestTaskTransitions(t *testing.T) {
	s, ft, ran, _ := setup(t)
	ctx := t.Context()
	dir := t.TempDir()
	other := t.TempDir()
	_, err := s.Replace(ctx, []api.Hook{{On: []string{api.HookTaskCreated, api.HookTaskDone, api.HookTaskReviewed,
		api.HookTaskBlocked}, Run: "echo hi", Enabled: true, Project: dir},
		{On: []string{api.HookTaskDone}, Run: "echo off", Enabled: false}})
	if err != nil {
		t.Fatal(err)
	}
	s.Start(false)
	base := api.TaskDetail{TaskSummary: api.TaskSummary{ID: "t1", Kind: task.KindNormal, Cwd: dir,
		Status: api.StatusQueued}}
	expect := func(event string) {
		t.Helper()
		select {
		case r := <-ran:
			if r.Event != event || r.TaskID != "t1" {
				t.Errorf("run = %s for %s, want %s", r.Event, r.TaskID, event)
			}
		case <-time.After(20 * time.Second):
			t.Fatalf("no %s run", event)
		}
	}
	ft.set(base, true)
	expect(api.HookTaskCreated)
	base.Status = api.StatusRunning
	ft.set(base, false)
	time.Sleep(200 * time.Millisecond) // task.started has no hook here
	base.Attention = &api.Attention{State: "blocked"}
	ft.set(base, false)
	expect(api.HookTaskBlocked)
	base.Status, base.Attention = api.StatusDone, nil
	ft.set(base, false)
	expect(api.HookTaskDone)
	base.Review = &api.Review{Verdict: api.VerdictAccepted}
	ft.set(base, false)
	expect(api.HookTaskReviewed)
	// Another project and a hidden run fire nothing.
	ft.set(api.TaskDetail{TaskSummary: api.TaskSummary{ID: "t2", Kind: task.KindNormal, Cwd: other,
		Status: api.StatusDone}}, true)
	ft.set(api.TaskDetail{TaskSummary: api.TaskSummary{ID: "t3", Kind: task.KindJudge, Cwd: dir,
		Status: api.StatusDone}}, true)
	select {
	case r := <-ran:
		t.Errorf("extra run %+v", r)
	case <-time.After(300 * time.Millisecond):
	}
}
