package ticket

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/gitx"
)

// TestPushAdoptsItsOwnLostPush is the re-review's P1: a push that reached GitHub while its recording on the ticket
// was lost (the daemon stopped, or the request went away, after GitHub took it). The next pr finds the commit in the
// ticket's own outbox and builds on it instead of calling the branch diverged.
func TestPushAdoptsItsOwnLostPush(t *testing.T) {
	e := setupFlow(t)
	ctx := t.Context()
	tk, task := readyTicket(t, e, "Lost")
	write(t, filepath.Join(tk.Worktree, "src", "cache.go"), "package src\n\nvar ttl = 21\n")
	run(t, tk.Worktree, "add", "-A")
	run(t, tk.Worktree, "commit", "-q", "-m", "worker one")
	e.tasks.accept(task, "ok")
	waitTicket(t, e.s, tk.ID, api.TicketReady)
	if _, err := e.s.PR(ctx, tk.ID, api.PRRequest{}); err != nil {
		t.Fatal(err)
	}
	first := e.gh.originSha(tk.Branch)
	if first == "" || first == run(t, tk.Worktree, "rev-parse", "HEAD") {
		t.Fatalf("origin has %q: want Seelegent's own commit", first)
	}
	// The recording is lost.
	if _, err := e.s.updateTicket(ctx, tk.ID, func(_ *sql.Tx, r *ticketRec) error {
		r.PushedSha, r.PushedBase = "", ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The worker commits again: the worktree's history and Seelegent's commit differ.
	write(t, filepath.Join(tk.Worktree, "src", "cache.go"), "package src\n\nvar ttl = 22\n")
	run(t, tk.Worktree, "add", "-A")
	run(t, tk.Worktree, "commit", "-q", "-m", "worker two")

	got, err := e.s.PR(ctx, tk.ID, api.PRRequest{})
	if err != nil {
		t.Fatalf("pr after a lost recording: %v", err)
	}
	second := e.gh.originSha(tk.Branch)
	if second == first || run(t, e.origin, "rev-parse", second+"^") != first {
		t.Fatalf("second push %s is not a fast-forward of %s", second, first)
	}
	if got.Refusal != nil || got.Error != "" || len(got.Exposed) != 0 {
		t.Fatalf("ticket after adoption: refusal %+v, error %q, exposed %v", got.Refusal, got.Error, got.Exposed)
	}
	if !hasLog(got, "push_adopted") {
		t.Fatal("no push_adopted step in the log")
	}
}

// cancelAfterPush is git that cancels the request right after a push returns: the caller went away while GitHub
// took the commit.
type cancelAfterPush struct {
	Git
	cancel context.CancelFunc
}

func (c cancelAfterPush) Exec(ctx context.Context, dir string, opt gitx.ExecOptions, args ...string) ([]byte, error) {
	out, err := c.Git.Exec(ctx, dir, opt, args...)
	if len(args) > 0 && args[0] == "push" {
		c.cancel()
	}
	return out, err
}

// TestPushRecordedWhenRequestEnds: the request is cancelled right after the push. The pushed commit is recorded
// with the outbox step all the same, and the next pr is a fast-forward on top of it.
func TestPushRecordedWhenRequestEnds(t *testing.T) {
	e := setupFlow(t)
	tk, task := readyTicket(t, e, "Cancelled")
	write(t, filepath.Join(tk.Worktree, "src", "cache.go"), "package src\n\nvar ttl = 31\n")
	e.tasks.accept(task, "ok")
	waitTicket(t, e.s, tk.ID, api.TicketReady)

	inner := e.s.o.Git
	rctx, cancel := context.WithCancel(t.Context())
	e.s.o.Git = cancelAfterPush{Git: inner, cancel: cancel}
	_, _ = e.s.PR(rctx, tk.ID, api.PRRequest{}) // the pull request step may fail: the caller is gone
	e.s.o.Git = inner
	first := e.gh.originSha(tk.Branch)
	if first == "" {
		t.Fatal("nothing was pushed")
	}
	rec, err := e.s.getTicket(t.Context(), tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.PushedSha != first {
		t.Fatalf("recorded push %q, origin has %s", rec.PushedSha, first)
	}

	write(t, filepath.Join(tk.Worktree, "src", "cache.go"), "package src\n\nvar ttl = 32\n")
	if _, err := e.s.PR(t.Context(), tk.ID, api.PRRequest{}); err != nil {
		t.Fatalf("pr after the cancelled request: %v", err)
	}
	if second := e.gh.originSha(tk.Branch); second == first || run(t, e.origin, "rev-parse", second+"^") != first {
		t.Fatalf("second push %s is not a fast-forward of %s", second, first)
	}
}

// TestPushAdoptsLegacyBranch: a branch the previous version pushed (the worker's own history, a deleted .env in it)
// is built on, and what it already published is kept on the ticket as a warning.
func TestPushAdoptsLegacyBranch(t *testing.T) {
	e := setupFlow(t)
	ctx := t.Context()
	tk, task := readyTicket(t, e, "Legacy")
	write(t, filepath.Join(tk.Worktree, ".env"), "AWS=AKIA"+"Z7Q4TESTCHECK2X9\n")
	run(t, tk.Worktree, "add", "-A")
	run(t, tk.Worktree, "commit", "-q", "-m", "oops")
	if err := os.Remove(filepath.Join(tk.Worktree, ".env")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(tk.Worktree, "src", "cache.go"), "package src\n\nvar ttl = 41\n")
	run(t, tk.Worktree, "add", "-A")
	run(t, tk.Worktree, "commit", "-q", "-m", "fix")
	// The previous version pushed the worker's history as it was.
	run(t, tk.Worktree, "push", "-q", e.origin, "HEAD:refs/heads/"+tk.Branch)
	legacy := e.gh.originSha(tk.Branch)
	e.tasks.accept(task, "ok")
	waitTicket(t, e.s, tk.ID, api.TicketReady)

	write(t, filepath.Join(tk.Worktree, "src", "extra.go"), "package src\n")
	got, err := e.s.PR(ctx, tk.ID, api.PRRequest{})
	if err != nil {
		t.Fatalf("pr on a legacy branch: %v", err)
	}
	if pushed := e.gh.originSha(tk.Branch); run(t, e.origin, "rev-parse", pushed+"^") != legacy {
		t.Fatalf("push %s does not build on the legacy branch %s", pushed, legacy)
	}
	if len(got.Exposed) != 1 || got.Exposed[0].Path != ".env" || got.Exposed[0].Kind != "env" {
		t.Fatalf("exposed = %+v", got.Exposed)
	}
	if got.Refusal != nil || got.NeedsAttention == api.TicketPushRefused {
		t.Fatalf("a warning, not a block: refusal %+v", got.Refusal)
	}
}

func hasLog(t api.Ticket, typ string) bool {
	for _, l := range t.Log {
		if l.Type == typ {
			return true
		}
	}
	return false
}
