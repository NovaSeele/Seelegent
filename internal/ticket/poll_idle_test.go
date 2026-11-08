package ticket

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/github"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/store"
)

// countingGitHub counts the access checks (each may run gh).
type countingGitHub struct {
	GitHub
	access atomic.Int32
}

func (c *countingGitHub) Access(ctx context.Context, force bool) github.Access {
	c.access.Add(1)
	return c.GitHub.Access(ctx, force)
}

// A poll with no connected repository and no open ticket checks nothing on GitHub, not even the access (M19: the
// idle daemon ran gh every githubPollMinutes for nothing).
func TestPollWithNothingToWatchRunsNoGh(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(t.Context(), filepath.Join(dir, "seelegent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	gh := &countingGitHub{GitHub: github.NewDriver(github.DriverOptions{
		Tokens: github.NewTokenStore(filepath.Join(dir, "github")), Gh: noGh{}})}
	s := New(Options{DB: db, Git: gitx.New(nil, nil), Tasks: newFakeTasks(), GitHub: gh,
		WorktreeBase: filepath.Join(dir, "worktrees"), UserHome: dir})
	if err := s.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := gh.access.Load(); n != 0 {
		t.Errorf("%d access checks with nothing to watch, want 0", n)
	}
}

// With a connected GitHub repository the poll still checks the access and syncs it.
func TestPollWithARepositoryChecksAccess(t *testing.T) {
	e := setupFlow(t)
	gh := &countingGitHub{GitHub: e.opts.GitHub}
	o := e.opts
	o.GitHub = gh
	s := New(o)
	if _, err := e.s.Connect(t.Context(), api.RepoConnectRequest{Path: e.repo}); err != nil {
		t.Fatal(err)
	}
	if err := s.Poll(t.Context()); err != nil {
		t.Logf("poll: %v", err) // the fake server may refuse parts of the sync; the access check is what counts
	}
	if gh.access.Load() == 0 {
		t.Error("no access check with a connected repository")
	}
}
