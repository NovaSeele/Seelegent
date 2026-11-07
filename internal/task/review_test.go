package task

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/brief"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/launch"
)

// TestFollowupResentAfterEarlyTransient retries a followup whose first attempt failed before the worker reported a
// session with the followup itself, not the bare retry prompt that would drop the lead's message.
func TestFollowupResentAfterEarlyTransient(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	h.queue.Push(t, okScript("conv-1", "first done"))
	sum := h.delegate(api.DelegateRequest{})
	h.waitStatus(sum.ID, StatusDone)
	h.queue.Push(t, transientScript())
	h.queue.Push(t, okScript("conv-1", "tests added"))
	if _, err := h.s.Followup(t.Context(), sum.ID, api.FollowupRequest{Message: "Now add the tests XYZZY."}); err != nil {
		t.Fatal(err)
	}
	h.waitEvent(sum.ID, api.EventRetry)
	h.waitFor("the second attempt", func() bool {
		h.clk.Advance(5 * time.Second)
		return h.queue.Left(t) == 0
	})
	h.waitStatus(sum.ID, StatusDone)
	p, err := h.s.Prompt(t.Context(), sum.ID, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if p == brief.Retry || !strings.Contains(p, "XYZZY") || strings.Contains(p, brief.Retry) {
		t.Fatalf("retry prompt = %q", p)
	}
}

// TestFollowupRetryCarriesRequest retries a followup whose failed attempt had already reported a session with the
// retry prompt plus the followup, for the case the request never reached the worker.
func TestFollowupRetryCarriesRequest(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	h.queue.Push(t, okScript("conv-2", "first done"))
	sum := h.delegate(api.DelegateRequest{})
	h.waitStatus(sum.ID, StatusDone)
	h.queue.Push(t, script(agyInit("conv-2"), "#!stderr Error: 503 UNAVAILABLE: the service is busy", "#!exit 1"))
	h.queue.Push(t, okScript("conv-2", "tests added"))
	if _, err := h.s.Followup(t.Context(), sum.ID, api.FollowupRequest{Message: "Now add the tests PLUGH."}); err != nil {
		t.Fatal(err)
	}
	h.waitEvent(sum.ID, api.EventRetry)
	h.waitFor("the second attempt", func() bool {
		h.clk.Advance(5 * time.Second)
		return h.queue.Left(t) == 0
	})
	h.waitStatus(sum.ID, StatusDone)
	p, err := h.s.Prompt(t.Context(), sum.ID, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p, brief.Retry) || !strings.Contains(p, "PLUGH") {
		t.Fatalf("retry prompt = %q", p)
	}
}

// TestShutdownDuringLaunchKeepsTask stops the daemon while a worker launch is starting: the task is not cancelled,
// and the next start recovers it to done.
func TestShutdownDuringLaunchKeepsTask(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	h.queue.Push(t, okScript("conv-s", "ok"))
	sum := h.delegate(api.DelegateRequest{})
	caught := false
	deadline := time.Now().Add(30 * time.Second)
	for !caught && time.Now().Before(deadline) {
		if a := lastOpenAttempt(h.task(sum.ID)); a != nil && a.LaunchDir != "" {
			f := launch.FilesIn(a.LaunchDir)
			if _, err := os.Stat(f.Spec); err == nil {
				if _, err := os.Stat(f.Ready); err != nil {
					caught = true
				}
			}
		}
		if !caught {
			time.Sleep(time.Millisecond)
		}
	}
	if !caught {
		t.Skip("did not catch the launch window")
	}
	h.stop()
	if tk := h.task(sum.ID); tk.Status == StatusCancelled || tk.LastTurn().Status == TurnCancelled {
		t.Fatalf("after the shutdown: task %s, turn %s", tk.Status, tk.LastTurn().Status)
	}
	h.start()
	if tk := h.waitStatus(sum.ID, StatusDone); tk.LastTurn().Response != "ok" {
		t.Fatalf("turn = %+v", tk.LastTurn())
	}
}

// TestCancelledTakeoverFreesSlot cancels a task whose takeover console never reports an exit: the account slot is
// released at once, so another task can run on the only account.
func TestCancelledTakeoverFreesSlot(t *testing.T) {
	h := newHarness(t)
	set := h.config()
	set.MaxConcurrentPerAccount = 1
	h.settings.Store(&set)
	h.agy("only", 0)
	h.queue.Push(t, okScript("conv-a", "a done"))
	a := h.delegate(api.DelegateRequest{})
	h.waitStatus(a.ID, StatusDone)
	if _, err := h.s.Takeover(t.Context(), a.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.s.Cancel(t.Context(), a.ID, api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	h.queue.Push(t, okScript("conv-b", "b done"))
	b := h.delegate(api.DelegateRequest{Prompt: "Another task."})
	h.waitStatus(b.ID, StatusDone)
	h.consoles.exit(0, 0)
}

// TestBaselineInBrief captures the files with uncommitted changes when the task starts and lists them in the brief.
func TestBaselineInBrief(t *testing.T) {
	gitPath, err := gitx.NewBinary(nil, nil).Path() // as the engine finds it: never a Windows batch shim
	if err != nil {
		t.Skipf("no usable git: %v", err)
	}
	h := newHarness(t)
	h.agy("only", 0)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), gitPath, args...)
		cmd.Dir = h.cwd
		cmd.Env = gitx.Env(os.Environ())
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init")
	if err := mkdir(filepath.Join(h.cwd, "sub")); err != nil {
		t.Fatal(err)
	}
	if err := writeText(filepath.Join(h.cwd, "sub", "wip [1].go"), "package sub\n"); err != nil {
		t.Fatal(err)
	}
	h.queue.Push(t, okScript("conv-b", "ok"))
	sum := h.delegate(api.DelegateRequest{})
	tk := h.waitStatus(sum.ID, StatusDone)
	if !tk.BaselineTaken || len(tk.Baseline) != 1 || tk.RepoRoot == "" {
		t.Fatalf("baseline = %v (taken %v), root %q", tk.Baseline, tk.BaselineTaken, tk.RepoRoot)
	}
	p, err := h.s.Prompt(t.Context(), sum.ID, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p, "- sub/wip [1].go") {
		t.Fatalf("the brief does not list the baseline:\n%s", p)
	}
}

// TestLeadInBrief (old F9): the session's lead is stored in lower case and the brief names it; an unknown lead is
// refused before anything is created.
func TestLeadInBrief(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	if _, err := h.s.Delegate(t.Context(), api.DelegateRequest{Prompt: "p", Cwd: h.cwd,
		Session: &api.Session{ID: "s1", Lead: "copilot"}}); !errors.Is(err, ErrInvalid) ||
		!strings.Contains(err.Error(), "session.lead") {
		t.Fatalf("unknown lead = %v", err)
	}
	h.queue.Push(t, okScript("conv-l", "ok"))
	sum := h.delegate(api.DelegateRequest{Session: &api.Session{ID: "s1", HostID: "host-uuid", Lead: "Codex"}})
	if sum.Session == nil || sum.Session.Lead != api.LeadCodex || sum.Session.HostID != "host-uuid" {
		t.Fatalf("session = %+v", sum.Session)
	}
	h.waitStatus(sum.ID, StatusDone)
	p, err := h.s.Prompt(t.Context(), sum.ID, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p, "\nThe lead for this task is Codex.\n") {
		t.Fatalf("the brief does not name the lead:\n%s", p)
	}
}

func TestParsePorcelainZ(t *testing.T) {
	root := filepath.FromSlash("/r")
	at := func(p ...string) []string {
		out := make([]string, len(p))
		for i, x := range p {
			out[i] = filepath.Join(root, filepath.FromSlash(x))
		}
		return out
	}
	cases := []struct {
		name, out string
		want      []string
	}{
		{"index rename", " M a.go\x00R  new.go\x00old.go\x00?? dir/x y.txt\x00", at("a.go", "new.go", "dir/x y.txt")},
		{"index copy", "C  copy.go\x00orig.go\x00 M b.go\x00", at("copy.go", "b.go")},
		{"worktree rename", " R moved.go\x00added.go\x00 M c.go\x00", at("moved.go", "c.go")},
		{"rename then edit", "RM n.go\x00o.go\x00", at("n.go")},
		{"added then copied", "AC k.go\x00j.go\x00?? z\x00", at("k.go", "z")},
	}
	for _, c := range cases {
		if got := parsePorcelainZ(root, []byte(c.out)); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// TestMappedListsNeverNull checks the real mappers: a task without prerequisites, spec task ids or files still
// gives [] lists in its summary, detail and page.
func TestMappedListsNeverNull(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	if err := mkdir(filepath.Join(h.cwd, "specs")); err != nil {
		t.Fatal(err)
	}
	h.queue.Push(t, okScript("conv-n", "ok"))
	sum := h.delegate(api.DelegateRequest{Spec: &api.SpecRequest{Dir: "specs"}})
	h.waitStatus(sum.ID, StatusDone)
	detail, err := h.s.Get(t.Context(), sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	page, err := h.s.List(t.Context(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]any{"summary": sum, "detail": detail, "page": page} {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"after", "taskIds", "files", "commands", "blockedCommands", "deniedActions"} {
			if strings.Contains(string(data), `"`+key+`":null`) {
				t.Errorf("%s: %s is null: %s", name, key, data)
			}
		}
	}
}
