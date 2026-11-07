package app

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/task"
	"github.com/NovaSeele/Seelegent/internal/testutil/fakecli"
)

func TestMain(m *testing.M) { fakecli.Main(m) }

const limit = 60 * time.Second

// daemon is one App on a test data directory whose agy CLI is the fake CLI.
type daemon struct {
	t     *testing.T
	paths config.Paths
	queue *fakecli.Queue
	cwd   string
	a     *App
}

func newDaemon(t *testing.T) *daemon {
	t.Helper()
	home := t.TempDir()
	paths, err := config.NewPaths(filepath.Join(home, "data"))
	if err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(home, "project")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	d := &daemon{t: t, paths: paths, queue: fakecli.NewQueue(t), cwd: cwd}
	d.open()
	return d
}

// open starts an App on the data directory (a daemon start).
func (d *daemon) open() {
	t := d.t
	t.Helper()
	set := config.Defaults()
	set.Port = 47831
	a, err := New(t.Context(), Options{
		Paths: d.paths, Settings: &set, Env: d.queue.Env(os.Environ()), RealHome: filepath.Join(d.paths.Home, "home"),
		Launcher: fakecli.Launcher(t), NoQuotaReaders: true,
		Bins: map[string]string{config.BackendAgy: fakecli.Exe(t), config.BackendClaude: "", config.BackendCodex: ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	d.a = a
	t.Cleanup(d.close)
}

func (d *daemon) close() {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	if err := d.a.Close(ctx); err != nil {
		d.t.Error(err)
	}
}

func (d *daemon) account(label string, priority int) account.Account {
	d.t.Helper()
	acc, _, err := d.a.Accounts.Add(d.t.Context(), account.New{Backend: config.BackendAgy, Kind: account.KindIsolated,
		Label: label, Priority: priority, Enabled: true}, nil)
	if err != nil {
		d.t.Fatal(err)
	}
	d.a.Pool.Notify()
	return acc
}

// wait waits for the tasks through the engine's own wait.
func (d *daemon) wait(ids ...string) api.WaitResult {
	d.t.Helper()
	ctx, cancel := context.WithTimeout(d.t.Context(), limit)
	defer cancel()
	res, err := d.a.Tasks.Wait(ctx, api.WaitRequest{IDs: ids, TimeoutSeconds: 60, WakeOnBlocked: new(false)}, "")
	if err != nil {
		d.t.Fatal(err)
	}
	if !res.Done {
		d.t.Fatalf("wait = %+v", res)
	}
	return res
}

func line(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func initLine(conv string) string {
	return line(map[string]any{"event": "init", "conversation_id": conv, "init": map[string]any{"model": "m"}})
}

func resultLine(conv, response string) string {
	return line(map[string]any{"event": "result", "result": map[string]any{"status": "SUCCESS", "response": response,
		"conversation_id": conv, "usage": map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}}})
}

func ok(conv, response string) string {
	return initLine(conv) + "\n" + resultLine(conv, response) + "\n"
}

// TestEndToEnd runs the engine as the daemon wires it, with the real launcher and the fake CLI: delegate, done,
// followup, review; a quota handoff; a cancel; and a restart that reattaches to a running worker.
func TestEndToEnd(t *testing.T) {
	d := newDaemon(t)
	first := d.account("first", 10)
	second := d.account("second", 0)
	ctx := t.Context()

	// delegate → done → followup → review
	d.queue.Push(t, ok("conv-1", "Turn one."))
	sum, err := d.a.Tasks.Delegate(ctx, api.DelegateRequest{Prompt: "Build the thing.", Cwd: d.cwd})
	if err != nil {
		t.Fatal(err)
	}
	if res := d.wait(sum.ID); res.Tasks[0].Status != task.StatusDone {
		t.Fatalf("first turn = %+v", res.Tasks[0])
	}
	d.queue.Push(t, ok("conv-1", "Turn two."))
	if _, err := d.a.Tasks.Followup(ctx, sum.ID, api.FollowupRequest{Message: "And the tests."}); err != nil {
		t.Fatal(err)
	}
	d.wait(sum.ID)
	rep, err := d.a.Tasks.Report(ctx, sum.ID, "")
	if err != nil || rep.Response != "Turn two." || rep.Turn != 2 ||
		!strings.HasPrefix(rep.Dashboard, "http://127.0.0.1:47831/tasks/") {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if _, err := d.a.Tasks.Review(ctx, sum.ID, api.ReviewRequest{Verdict: api.VerdictAccepted}); err != nil {
		t.Fatal(err)
	}

	// quota handoff
	d.queue.Push(t, initLine("conv-q")+"\n"+line(map[string]any{"event": "result", "result": map[string]any{
		"status": "ERROR", "error": "RESOURCE_EXHAUSTED: quota exhausted, resets in 2h"}})+"\n#!exit 1\n")
	d.queue.Push(t, ok("conv-q2", "Done elsewhere."))
	q, err := d.a.Tasks.Delegate(ctx, api.DelegateRequest{Prompt: "Use quota.", Cwd: d.cwd})
	if err != nil {
		t.Fatal(err)
	}
	d.wait(q.ID)
	detail, err := d.a.Tasks.Get(ctx, q.ID)
	if err != nil {
		t.Fatal(err)
	}
	at := detail.Turns[0].Attempts
	if len(at) != 2 || at[0].AccountID != first.ID || at[0].Status != task.AttemptQuota || at[1].AccountID != second.ID {
		t.Fatalf("attempts = %+v", at)
	}

	// cancel (the first account rests now, so the second runs it)
	d.queue.Push(t, initLine("conv-c")+"\n#!hang\n")
	c, err := d.a.Tasks.Delegate(ctx, api.DelegateRequest{Prompt: "Hang.", Cwd: d.cwd})
	if err != nil {
		t.Fatal(err)
	}
	waitLive(t, d.a, c.ID)
	cs, stopped, err := d.a.Tasks.Cancel(ctx, c.ID, api.CancelRequest{WaitSeconds: 30})
	if err != nil || !stopped || cs.Status != task.StatusCancelled {
		t.Fatalf("cancel = %+v %v %v", cs.Status, stopped, err)
	}

	// restart while a worker runs: the next daemon attaches to it
	release := filepath.Join(d.paths.Home, "release")
	d.queue.Push(t, initLine("conv-r")+"\n#!wait-file "+release+"\n"+resultLine("conv-r", "Survived.")+"\n")
	r, err := d.a.Tasks.Delegate(ctx, api.DelegateRequest{Prompt: "Survive.", Cwd: d.cwd})
	if err != nil {
		t.Fatal(err)
	}
	waitLive(t, d.a, r.ID)
	d.close()
	d.open()
	waitLive(t, d.a, r.ID)
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	res := d.wait(r.ID)
	if res.Tasks[0].Status != task.StatusDone {
		t.Fatalf("after restart = %+v", res.Tasks[0])
	}
	page, err := d.a.Tasks.Events(ctx, r.ID, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	reattached := false
	for _, e := range page.Items {
		reattached = reattached || e.Type == api.EventReattach
	}
	if !reattached || len(d.queue.Records(t)) != 6 {
		t.Fatalf("reattach event %v, runs %d", reattached, len(d.queue.Records(t)))
	}
}

func waitLive(t *testing.T, a *App, id string) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for {
		for _, w := range a.Tasks.LiveWorkers() {
			if w.TaskID == id {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no live worker for %s", id)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestInstallShimKeepsWorkingShim: a shim install that fails keeps the shim of an earlier run when it still works,
// and leaves workers without a shim only when there is none.
func TestInstallShimKeepsWorkingShim(t *testing.T) {
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		t.Skip("root ignores the folder permissions this test relies on")
	}
	base := t.TempDir()
	exe := filepath.Join(base, "bin", "seelegent.exe")
	if err := os.MkdirAll(filepath.Dir(exe), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	dir := filepath.Join(base, "guard", "bin")
	if got := installShim(log, dir, exe); got != dir || logs.Len() != 0 {
		t.Fatalf("first install: %q, logs %s", got, logs.String())
	}
	// The next install fails: on Windows the new executable cannot be read (as when a running git.exe blocks the
	// rename), elsewhere the folder is read-only.
	other := filepath.Join(base, "bin", "missing.exe")
	if runtime.GOOS != "windows" {
		other = filepath.Join(base, "bin", "seelegent-new")
		if err := os.WriteFile(other, []byte("binary"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	}
	if got := installShim(log, dir, other); got != dir || !strings.Contains(logs.String(), "keeping the one") {
		t.Fatalf("failed install with a working shim: %q, logs %s", got, logs.String())
	}
	// Without an earlier shim there is nothing to keep.
	logs.Reset()
	fresh := filepath.Join(base, "fresh")
	if runtime.GOOS != "windows" {
		if err := os.MkdirAll(fresh, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(fresh, 0o700) })
		fresh = filepath.Join(fresh, "bin")
	}
	if got := installShim(log, fresh, other); got != "" || !strings.Contains(logs.String(), "unguarded") {
		t.Fatalf("failed install without a shim: %q, logs %s", got, logs.String())
	}
}

func TestNewNeedsPaths(t *testing.T) {
	if _, err := New(t.Context(), Options{}); err == nil {
		t.Fatal("no error without a data directory")
	}
}
