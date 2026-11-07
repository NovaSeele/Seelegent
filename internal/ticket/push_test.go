package ticket

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/gitx"
)

// readyTicket connects the repository, starts a ticket for a local issue and returns it with its task.
func readyTicket(t *testing.T, e *env, title string) (tk api.Ticket, taskID string) {
	t.Helper()
	ctx := t.Context()
	r, err := e.s.Connect(ctx, api.RepoConnectRequest{Path: e.repo})
	if err != nil && !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if r.ID == "" {
		repos, err := e.s.Repos(ctx)
		if err != nil || len(repos) == 0 {
			t.Fatalf("repos: %v", err)
		}
		r = repos[0]
	}
	it, err := e.s.AddIssue(ctx, r.ID, api.IssueInput{Title: title})
	if err != nil {
		t.Fatal(err)
	}
	tr, err := e.s.Triage(ctx, r.ID, it.Key, api.TriageRequest{Action: api.TriageAssign})
	if err != nil {
		t.Fatal(err)
	}
	tk, err = e.s.Ticket(ctx, tr.Ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	return tk, tr.TaskID
}

// originObjects is the content of every object in the bare origin.
func originObjects(t *testing.T, origin string) string {
	t.Helper()
	return run(t, origin, "cat-file", "--batch-all-objects", "--batch", "--unordered")
}

// TestPushNeverSendsWorkerHistory is the review's B1: a worker commits a secret, then deletes it in a later
// commit. The pull request must not carry that history: origin gets one commit of Seelegent's of the final tree.
func TestPushNeverSendsWorkerHistory(t *testing.T) {
	e := setupFlow(t)
	ctx := t.Context()
	// A pre-push hook of the user's: never run by Seelegent's push (G5).
	marker := filepath.Join(t.TempDir(), "pre-push-ran")
	write(t, filepath.Join(e.repo, ".git", "hooks", "pre-push"), "#!/bin/sh\necho ran > '"+
		filepath.ToSlash(marker)+"'\n")
	if err := os.Chmod(filepath.Join(e.repo, ".git", "hooks", "pre-push"), 0o700); err != nil {
		t.Fatal(err)
	}
	tk, task := readyTicket(t, e, "Config")
	wt := tk.Worktree
	aws := "AKIA" + "Z7Q4TESTCHECK2X9"
	write(t, filepath.Join(wt, ".env"), "AWS="+aws+"\n")
	write(t, filepath.Join(wt, "cfg.txt"), "token = "+testToken+"\n")
	run(t, wt, "add", "-A")
	run(t, wt, "commit", "-q", "-m", "oops")
	if err := os.Remove(filepath.Join(wt, ".env")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(wt, "cfg.txt"), "token = none\n")
	run(t, wt, "add", "-A")
	run(t, wt, "commit", "-q", "-m", "fix")
	e.tasks.accept(task, "ok")
	waitTicket(t, e.s, tk.ID, api.TicketReady)

	tk, err := e.s.PR(ctx, tk.ID, api.PRRequest{})
	if err != nil {
		t.Fatal(err)
	}
	first := e.gh.originSha(tk.Branch)
	if first == "" || first == run(t, wt, "rev-parse", "HEAD") {
		t.Fatalf("origin has %q: want Seelegent's own commit", first)
	}
	if parents := run(t, e.origin, "rev-list", "--parents", "-n", "1", first); parents != first+" "+tk.Base &&
		parents != first+" "+run(t, e.repo, "rev-parse", "main") {
		t.Fatalf("pushed commit parents: %s", parents)
	}
	if got, want := run(t, e.origin, "rev-parse", first+"^{tree}"), run(t, wt, "rev-parse", "HEAD^{tree}"); got != want {
		t.Fatalf("pushed tree %s, worktree tree %s", got, want)
	}
	all := originObjects(t, e.origin)
	if strings.Contains(all, testToken) || strings.Contains(all, aws) {
		t.Fatal("origin holds an object with the secret")
	}
	if tk.PR == nil || tk.PR.HeadSha != first {
		t.Fatalf("pr = %+v, want head %s", tk.PR, first)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the user's pre-push hook ran")
	}

	// A follow-up change: the next push is a fast-forward on top of the first.
	write(t, filepath.Join(wt, "src", "cache.go"), "package src\n\nvar ttl = 30\n")
	tk, err = e.s.PR(ctx, tk.ID, api.PRRequest{})
	if err != nil {
		t.Fatal(err)
	}
	second := e.gh.originSha(tk.Branch)
	if second == first || run(t, e.origin, "rev-parse", second+"^") != first {
		t.Fatalf("second push %s is not a fast-forward of %s", second, first)
	}
	// Nothing new: no push.
	if tk, err = e.s.PR(ctx, tk.ID, api.PRRequest{}); err != nil || e.gh.originSha(tk.Branch) != second {
		t.Fatalf("pr without changes: %v", err)
	}
}

// TestPushScanRefusesUncommittedAndCommitted checks the scan of what the pushed commit adds: a secret committed by
// the worker and still in the tree is refused like an uncommitted one, and nothing is pushed.
func TestPushScanRefusesCommittedSecret(t *testing.T) {
	e := setupFlow(t)
	tk, task := readyTicket(t, e, "Keys")
	write(t, filepath.Join(tk.Worktree, "deploy.key"), "x\n")
	write(t, filepath.Join(tk.Worktree, "src", "cfg.go"), "package src\n\nconst k = \""+testToken+"\"\n")
	run(t, tk.Worktree, "add", "-A")
	run(t, tk.Worktree, "commit", "-q", "-m", "keys")
	e.tasks.accept(task, "ok")
	waitTicket(t, e.s, tk.ID, api.TicketReady)
	_, err := e.s.PR(t.Context(), tk.ID, api.PRRequest{})
	var se *ScanError
	if !errors.As(err, &se) || len(se.Findings) != 2 {
		t.Fatalf("pr = %v", err)
	}
	if e.gh.originSha(tk.Branch) != "" {
		t.Fatal("pushed after a refused scan")
	}
}

// brokenCatFile is git with a cat-file that fails: the scan cannot read what it must check.
type brokenCatFile struct{ Git }

func (b brokenCatFile) Exec(ctx context.Context, dir string, opt gitx.ExecOptions, args ...string) ([]byte, error) {
	if len(args) > 0 && args[0] == "cat-file" && opt.Stdin != nil {
		return nil, errors.New("fatal: unable to read objects")
	}
	return b.Git.Exec(ctx, dir, opt, args...)
}

// TestPushRefusedWhenScanFails is the review's G8: a scan that cannot run refuses the pull request; nothing is
// committed or pushed unscanned.
func TestPushRefusedWhenScanFails(t *testing.T) {
	e := setupFlow(t)
	tk, task := readyTicket(t, e, "Broken")
	write(t, filepath.Join(tk.Worktree, "src", "cache.go"), "package src\n\nvar ttl = 3\n")
	e.tasks.accept(task, "ok")
	waitTicket(t, e.s, tk.ID, api.TicketReady)
	e.s.o.Git = brokenCatFile{e.s.o.Git}
	_, err := e.s.PR(t.Context(), tk.ID, api.PRRequest{})
	var pe *PushError
	if !errors.As(err, &pe) || pe.Reason != "scan_failed" || !errors.Is(err, ErrScan) ||
		!strings.Contains(err.Error(), "could not scan") {
		t.Fatalf("pr with a broken scan: %v", err)
	}
	if e.gh.originSha(tk.Branch) != "" || run(t, tk.Worktree, "status", "--porcelain") == "" {
		t.Fatal("committed or pushed without a scan")
	}
}

// TestPushRemoteRules:a remote branch someone changed is never built on (the push entry fails), and a base with
// commits the remote does not have is refused.
func TestPushRemoteRules(t *testing.T) {
	e := setupFlow(t)
	ctx := t.Context()
	tk, task := readyTicket(t, e, "Remote")
	write(t, filepath.Join(tk.Worktree, "src", "cache.go"), "package src\n\nvar ttl = 7\n")
	e.tasks.accept(task, "ok")
	waitTicket(t, e.s, tk.ID, api.TicketReady)
	if _, err := e.s.PR(ctx, tk.ID, api.PRRequest{}); err != nil {
		t.Fatal(err)
	}
	// Someone updates the branch on GitHub.
	other := filepath.Join(t.TempDir(), "other")
	run(t, filepath.Dir(other), "clone", "-q", "-b", tk.Branch, e.origin, other)
	run(t, other, "-c", "user.name=O", "-c", "user.email=o@x", "commit", "-q", "--allow-empty", "-m", "theirs")
	run(t, other, "push", "-q", "origin", tk.Branch)
	theirs := e.gh.originSha(tk.Branch)
	write(t, filepath.Join(tk.Worktree, "src", "cache.go"), "package src\n\nvar ttl = 8\n")
	_, err := e.s.PR(ctx, tk.ID, api.PRRequest{})
	var pe *PushError
	if !errors.As(err, &pe) || pe.Reason != "branch_diverged" || !errors.Is(err, ErrConflict) {
		t.Fatalf("pr on a changed branch: %v", err)
	}
	if e.gh.originSha(tk.Branch) != theirs {
		t.Fatal("the changed branch was overwritten")
	}
	got, _ := e.s.Ticket(ctx, tk.ID)
	last := got.Outbox[len(got.Outbox)-1]
	if last.Kind != api.OutboxPush || last.Status != api.OutboxFailed {
		t.Fatalf("last outbox entry = %+v", last)
	}

	// A base the remote does not have: the user's main has a commit that was never pushed.
	write(t, filepath.Join(e.repo, "local.txt"), "not pushed\n")
	run(t, e.repo, "add", "local.txt")
	run(t, e.repo, "commit", "-q", "-m", "local only")
	tk2, task2 := readyTicket(t, e, "Second")
	write(t, filepath.Join(tk2.Worktree, "src", "cache.go"), "package src\n\nvar ttl = 9\n")
	e.tasks.accept(task2, "ok")
	waitTicket(t, e.s, tk2.ID, api.TicketReady)
	_, err = e.s.PR(ctx, tk2.ID, api.PRRequest{})
	if !errors.As(err, &pe) || pe.Reason != "base_not_pushed" || pe.Details["commits"] != 1 {
		t.Fatalf("pr on an unpushed base: %v", err)
	}
	if e.gh.originSha(tk2.Branch) != "" {
		t.Fatal("pushed on an unpushed base")
	}
	// Once main is pushed, the pull request goes.
	run(t, e.repo, "push", "-q", e.origin, "main")
	if _, err := e.s.PR(ctx, tk2.ID, api.PRRequest{}); err != nil {
		t.Fatal(err)
	}
}

// TestPushAfterBaseMoved: main was merged into the worktree between two pushes. The next commit has two parents
// (the commit pushed before and the new base), and only what is new relative to both is scanned: a token fixture
// main brought in does not refuse the pull request.
func TestPushAfterBaseMoved(t *testing.T) {
	e := setupFlow(t)
	ctx := t.Context()
	tk, task := readyTicket(t, e, "Moved")
	write(t, filepath.Join(tk.Worktree, "src", "cache.go"), "package src\n\nvar ttl = 11\n")
	e.tasks.accept(task, "ok")
	waitTicket(t, e.s, tk.ID, api.TicketReady)
	if _, err := e.s.PR(ctx, tk.ID, api.PRRequest{}); err != nil {
		t.Fatal(err)
	}
	first := e.gh.originSha(tk.Branch)
	// main moves on GitHub with a test fixture that holds a token-shaped string.
	write(t, filepath.Join(e.repo, "fixtures", "token.txt"), testToken+"\n")
	run(t, e.repo, "add", "fixtures/token.txt")
	run(t, e.repo, "commit", "-q", "-m", "fixture")
	run(t, e.repo, "push", "-q", e.origin, "main")
	newBase := run(t, e.repo, "rev-parse", "main")
	run(t, tk.Worktree, "merge", "-q", "--no-edit", "main")
	write(t, filepath.Join(tk.Worktree, "src", "extra.go"), "package src\n")

	tk, err := e.s.PR(ctx, tk.ID, api.PRRequest{})
	if err != nil {
		t.Fatal(err)
	}
	second := e.gh.originSha(tk.Branch)
	if parents := run(t, e.origin, "rev-list", "--parents", "-n", "1", second); parents != second+" "+first+" "+newBase {
		t.Fatalf("parents = %s, want %s %s", parents, first, newBase)
	}
	// A token added by the ticket itself is still refused.
	write(t, filepath.Join(tk.Worktree, "src", "leak.go"), "package src\n\nconst t = \""+testToken+"\"\n")
	var se *ScanError
	if _, err := e.s.PR(ctx, tk.ID, api.PRRequest{}); !errors.As(err, &se) || len(se.Findings) != 1 ||
		se.Findings[0].Path != "src/leak.go" {
		t.Fatalf("pr with the ticket's own token: %v", err)
	}
}
