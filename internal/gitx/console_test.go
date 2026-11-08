package gitx

import (
	"strings"
	"testing"
)

// TestRunsUserCode: a git that can run hooks, filters or credential helpers is recognized after any of git's own
// options (so it gets a console of its own), and the read-only commands the daemon runs all the time are not.
func TestRunsUserCode(t *testing.T) {
	for _, c := range []struct {
		args       string
		sub        string
		user, hook bool
	}{
		{"--literal-pathspecs add -- a.txt", "add", true, false},
		{"--literal-pathspecs commit --only -m msg -- a.txt", "commit", true, true},
		{"-c user.name=x -c commit.gpgsign=false commit -m m", "commit", true, true},
		{"-c core.hooksPath=NUL --no-pager -C dir --git-dir .git --work-tree=. push origin b", "push", true, true},
		{"--git-dir .git worktree add --detach d HEAD", "worktree", true, true},
		{"--namespace ns --config-env a=B fetch origin", "fetch", true, false},
		{"checkout main", "checkout", true, false},
		{"reset --hard", "reset", true, false},
		{"stash push", "stash", true, false},
		{"hash-object --stdin --path=a.txt", "hash-object", true, false},
		{"hash-object a.txt", "hash-object", true, false},
		{"hash-object --stdin", "hash-object", false, false},
		{"hash-object --no-filters a.txt", "hash-object", false, false},
		{"-c core.quotepath=off --literal-pathspecs status --porcelain -z", "status", false, false},
		{"diff --no-color --no-ext-diff --no-textconv HEAD --", "diff", false, false},
		{"rev-parse --verify HEAD", "rev-parse", false, false},
		{"ls-files -z", "ls-files", false, false},
		{"cat-file blob HEAD:a", "cat-file", false, false},
		{"show --shortstat --format= HEAD", "show", false, false},
		{"--version", "", false, false},
		{"-c commit.gpgsign=false", "", false, false},
		{"-C", "", false, false},
		{"-- commit", "", false, false},
		{"", "", false, false},
	} {
		args := strings.Fields(c.args)
		if got := subcommand(args); got != c.sub {
			t.Errorf("subcommand(%q) = %q, want %q", c.args, got, c.sub)
		}
		if got := runsUserCode(args); got != c.user {
			t.Errorf("runsUserCode(%q) = %v, want %v", c.args, got, c.user)
		}
		if got := runsHooks(args); got != c.hook {
			t.Errorf("runsHooks(%q) = %v, want %v", c.args, got, c.hook)
		}
	}
}
