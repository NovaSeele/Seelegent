package ticket

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/github"
)

// countingGitHub counts the access checks (each one may run gh twice and call GitHub).
type countingGitHub struct {
	GitHub
	access atomic.Int32
}

func (c *countingGitHub) Access(ctx context.Context, force bool) github.Access {
	c.access.Add(1)
	return c.GitHub.Access(ctx, force)
}

// A poll with no open ticket and no GitHub repository never asks for the access tier: the 10-minute poll of an
// idle daemon runs no gh and calls no GitHub (M19 review).
func TestPollWithNothingToPollSkipsAccess(t *testing.T) {
	e := setupFlow(t)
	ctx := t.Context()
	gh := &countingGitHub{GitHub: e.opts.GitHub}
	o := e.opts
	o.GitHub = gh
	s := New(o)
	if err := s.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if n := gh.access.Load(); n != 0 {
		t.Fatalf("empty poll asked for access %d times", n)
	}
	// A GitHub repository to sync: the poll asks (and the sync it runs asks again, from the cache).
	if _, err := e.s.Connect(ctx, api.RepoConnectRequest{Path: e.repo}); err != nil {
		t.Fatal(err)
	}
	if err := s.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if n := gh.access.Load(); n == 0 {
		t.Fatal("poll with a repository never asked for access")
	}
}
