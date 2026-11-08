package ticket

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/github"
	"github.com/NovaSeele/Seelegent/internal/gitx"
)

// TestErrorCause: a failed step gets a cause the dashboard explains in the reader's language; git's own messages
// for a sign-in it cannot make (old D18: the push uses the user's git credentials, not the token), a rejected push
// and a remote it cannot reach are told apart.
func TestErrorCause(t *testing.T) {
	git := func(msg string) error { return &GitError{Op: "push", Message: msg} }
	for _, c := range []struct {
		err  error
		want string
	}{
		{git("fatal: could not read Username for 'https://github.com': terminal prompts disabled"), api.CauseGitAuth},
		{git("remote: Invalid username or password. fatal: Authentication failed for 'https://github.com/o/r.git/'"),
			api.CauseGitAuth},
		{git("git@github.com: Permission denied (publickey). fatal: Could not read from remote repository."),
			api.CauseGitAuth},
		{git("remote: Permission to o/r.git denied to someone. fatal: unable to access 'https://github.com/o/r.git/': " +
			"The requested URL returned error: 403"), api.CauseGitAuth},
		{git("! [remote rejected] abc -> seelegent/1-x (protected branch hook declined) error: failed to push some refs"),
			api.CauseGitRejected},
		{git("fatal: unable to access 'https://github.com/o/r.git/': Could not resolve host: github.com"),
			api.CauseNetwork},
		{git("fatal: bad object abc"), api.CauseGit},
		{&github.Error{Reason: github.ReasonAuth, Status: 401, Message: "Bad credentials"}, api.CauseGitHubAuth},
		{&github.Error{Reason: github.ReasonRateLimited, Status: 403}, api.CauseGitHubRateLimited},
		{&github.Error{Reason: github.ReasonNotFound, Status: 404}, api.CauseGitHubNotFound},
		{&github.Error{Reason: github.ReasonNetwork}, api.CauseNetwork},
		{&github.Error{Reason: github.ReasonInvalid, Status: 422}, api.CauseGitHub},
		{fmt.Errorf("open pr: %w", github.ErrManual), api.CauseGitHubManual},
		{context.DeadlineExceeded, api.CauseNetwork},
		{errors.New("something else"), api.CauseOther},
		{nil, ""},
	} {
		if got := errorCause(c.err); got != c.want {
			t.Errorf("%v: %q, want %q", c.err, got, c.want)
		}
	}
}

// failingPush is git whose push fails as git does without credentials.
type failingPush struct{ Git }

func (f failingPush) Exec(ctx context.Context, dir string, opt gitx.ExecOptions, args ...string) ([]byte, error) {
	if len(args) > 0 && args[0] == "push" {
		return nil, errors.New("fatal: could not read Username for 'https://github.com': terminal prompts disabled")
	}
	return f.Git.Exec(ctx, dir, opt, args...)
}

// TestPushAuthFailureHasCause: a push git cannot sign in for fails its outbox step and the ticket with the cause
// git_auth (old D18), so the dashboard can say that git needs credentials; the English text stays for the lead.
func TestPushAuthFailureHasCause(t *testing.T) {
	e := setupFlow(t)
	tk, task := readyTicket(t, e, "No credentials")
	write(t, filepath.Join(tk.Worktree, "src", "cache.go"), "package src\n\nvar ttl = 4\n")
	e.tasks.accept(task, "ok")
	waitTicket(t, e.s, tk.ID, api.TicketReady)
	e.s.o.Git = failingPush{e.s.o.Git}
	if _, err := e.s.PR(t.Context(), tk.ID, api.PRRequest{}); err == nil {
		t.Fatal("pr without git credentials pushed")
	}
	got, err := e.s.Ticket(t.Context(), tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Error == "" || got.ErrorCause != api.CauseGitAuth || got.Refusal != nil {
		t.Fatalf("ticket error %q cause %q refusal %+v", got.Error, got.ErrorCause, got.Refusal)
	}
	var push *api.OutboxEntry
	for i := range got.Outbox {
		if got.Outbox[i].Kind == api.OutboxPush {
			push = &got.Outbox[i]
		}
	}
	if push == nil || push.Status != api.OutboxFailed || push.Cause != api.CauseGitAuth || push.Error == "" {
		t.Fatalf("push entry = %+v", push)
	}
	// A list of tickets shows the cause too; a ticket without an error has none.
	list, err := e.s.Tickets(t.Context(), "", nil)
	if err != nil || len(list) != 1 || list[0].ErrorCause != api.CauseGitAuth {
		t.Fatalf("list = %+v %v", list, err)
	}
}
