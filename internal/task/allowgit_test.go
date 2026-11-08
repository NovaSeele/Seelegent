package task

import (
	"path/filepath"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/testutil/fakecli"
)

// guardOff reports whether worker run i had the git guard turned off (allow_git).
func (h *harness) guardOff(i int) bool {
	h.t.Helper()
	recs := h.queue.Records(h.t)
	if i >= len(recs) {
		h.t.Fatalf("worker run %d of %d", i, len(recs))
	}
	return recs[i].Env[account.EnvGitGuard] == "off"
}

// TestAllowGitPerCommand: allow_git applies to the command that passed it — its turn and that command's automatic
// fix turns — and a followup without it runs guarded (it never stays on for the rest of the task).
func TestAllowGitPerCommand(t *testing.T) {
	t.Setenv(fakecli.EnvRecord, account.EnvGitGuard)
	h := newHarness(t)
	h.set(func(s *config.Settings) { s.GateMaxAutoFixes = 1; s.GitGuard = true })
	h.initRepo(map[string]string{"a.txt": "broken\n"})
	h.agy("w", 1)
	a := filepath.Join(h.cwd, "a.txt")
	h.queue.Push(t, editScript("conv-ag", a, "still broken\n", "Changed a.txt."))
	h.queue.Push(t, editScript("conv-ag", a, "fixed\n", "Fixed a.txt."))
	sum := h.delegate(api.DelegateRequest{Title: "commit", AllowGit: true,
		GateCommands: []string{gateCmd(t, "a.txt", "fixed")}})
	tk := h.waitStatus(sum.ID, StatusDone)
	if len(tk.Turns) != 2 || tk.Turns[1].By != ByGate {
		t.Fatalf("turns = %d, the second by %q", len(tk.Turns), tk.Turns[len(tk.Turns)-1].By)
	}
	if !h.guardOff(0) || !h.guardOff(1) || !tk.Turns[0].AllowGit || !tk.Turns[1].AllowGit {
		t.Fatalf("delegate with allow_git: guard off %v/%v, turns %v/%v", h.guardOff(0), h.guardOff(1),
			tk.Turns[0].AllowGit, tk.Turns[1].AllowGit)
	}

	yes, no := true, false
	for i, c := range []struct {
		allow *bool
		want  bool
	}{{nil, false}, {&yes, true}, {nil, false}, {&no, false}} {
		h.queue.Push(t, okScript("conv-ag", "Done."))
		if _, err := h.s.Followup(t.Context(), sum.ID, api.FollowupRequest{Message: "Next.", AllowGit: c.allow}); err != nil {
			t.Fatal(err)
		}
		tk = h.waitStatus(sum.ID, StatusDone)
		n := 2 + i
		if got := h.guardOff(n); got != c.want || tk.LastTurn().AllowGit != c.want {
			t.Errorf("followup %d (allow_git %v): guard off %v, turn %v; want %v", i+1, c.allow, got,
				tk.LastTurn().AllowGit, c.want)
		}
	}
}

// TestAllowGitSurvivesRestart: the in-flight command keeps its allow_git across a daemon restart (it is stored on
// the turn), so the worker the next daemon starts for it runs unguarded.
func TestAllowGitSurvivesRestart(t *testing.T) {
	t.Setenv(fakecli.EnvRecord, account.EnvGitGuard)
	h := newHarness(t)
	h.set(func(s *config.Settings) { s.GitGuard = true })
	sum := h.delegate(api.DelegateRequest{AllowGit: true})
	h.waitFor("waiting_quota", func() bool { return h.task(sum.ID).Status == StatusWaitingQuota })
	h.stop()
	h.agy("late", 0)
	h.queue.Push(t, okScript("conv-r", "ok"))
	h.start()
	h.waitFor("the task to run", func() bool {
		h.clk.Advance(noAccountRecheck)
		return h.task(sum.ID).Status == StatusDone
	})
	if !h.guardOff(0) {
		t.Fatal("the worker started after the restart ran guarded")
	}
}
